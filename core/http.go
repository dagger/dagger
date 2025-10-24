package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/sources/netconfhttp"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

//nolint:gocyclo
func DoHTTPRequest(
	ctx context.Context,
	query *Query,
	req *http.Request,
	filename string,
	permissions int,
) (_ bkcache.ImmutableRef, _ digest.Digest, _ *http.Response, rerr error) {
	cache := query.BuildkitCache()

	// FIXME: this is the same as the legacy buildkit behavior, but we *could*
	// potentially reuse ETags even if filename/permissions change: then
	// creating a new snapshot, copying only the data from the old one
	url := req.URL.String()
	urlDigest := hashutil.HashStrings(url, filename, fmt.Sprint(permissions))

	mds, err := searchHTTPByDigest(ctx, cache, urlDigest)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to search metadata for %s: %w", url, err)
	}

	// m is etag->metadata
	m := map[string]cacheRefMetadata{}

	// If we request a single ETag in 'If-None-Match', some servers omit the
	// unambiguous ETag in their response.
	// See: https://github.com/dagger/dagger/internal/buildkit/issues/905
	var onlyETag string

	if len(mds) > 0 {
		for _, md := range mds {
			if etag := md.getETag(); etag != "" {
				if dgst := md.getHTTPChecksum(); dgst != "" {
					m[etag] = md
				}
			}
		}
		if len(m) > 0 {
			etags := make([]string, 0, len(m))
			for t := range m {
				etags = append(etags, t)
			}
			req.Header.Add("If-None-Match", strings.Join(etags, ", "))

			if len(etags) == 1 {
				onlyETag = etags[0]
			}
		}
	}

	dns, err := DNSConfig(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	client := http.Client{
		Transport: netconfhttp.NewTransport(http.DefaultTransport, dns),
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, "", nil, fmt.Errorf("invalid response status %s", resp.Status)
	}
	if resp.StatusCode == http.StatusNotModified {
		respETag := etagValue(resp.Header.Get("ETag"))

		if respETag == "" && onlyETag != "" {
			// handle poorly behaving servers that don't return the ETag when
			// there's only one provided :(
			respETag = onlyETag
			resp.Header.Set("ETag", onlyETag)
		}

		md, ok := m[respETag]
		if !ok {
			return nil, "", nil, fmt.Errorf("invalid not-modified ETag: %v", respETag)
		}
		dgst := md.getHTTPChecksum()
		if dgst == "" {
			return nil, "", nil, fmt.Errorf("invalid metadata change")
		}
		if modTime := md.getHTTPModTime(); modTime != "" {
			resp.Header.Set("Last-Modified", modTime)
		}

		snap, err := cache.Get(ctx, md.ID(), nil)
		if err != nil {
			return nil, "", nil, err
		}
		return snap, md.getHTTPChecksum(), resp, nil
	}

	bkref, err := cache.New(ctx, nil, nil,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("http url %s", url)))
	if err != nil {
		return nil, "", nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	h := sha256.New()
	err = MountRef(ctx, bkref, nil, func(out string) error {
		// create the file
		dest := filepath.Join(out, filename)
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(permissions))
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}

		// update file atime+mtime to the last-modified time of the response
		timestamp := time.Unix(0, 0)
		if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
			if parsedMTime, err := http.ParseTime(lastMod); err == nil {
				timestamp = parsedMTime
			}
		}
		if err := os.Chtimes(dest, timestamp, timestamp); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("file write failed: %w", err)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	defer snap.Release(context.WithoutCancel(ctx))
	bkref = nil

	contentDgst := digest.NewDigest(digest.SHA256, h)

	md := cacheRefMetadata{snap}
	if respETag := resp.Header.Get("ETag"); respETag != "" {
		respETag = etagValue(respETag)
		if err := md.setETag(respETag); err != nil {
			return nil, "", nil, err
		}
		if err := md.setHTTPChecksum(urlDigest, contentDgst); err != nil {
			return nil, "", nil, err
		}
	}
	if modTime := resp.Header.Get("Last-Modified"); modTime != "" {
		if err := md.setHTTPModTime(modTime); err != nil {
			return nil, "", nil, err
		}
	}

	resp.Body = io.NopCloser(bytes.NewReader(nil))
	return snap, contentDgst, resp, nil
}

func etagValue(v string) string {
	// remove weak for direct comparison
	return strings.TrimPrefix(v, "W/")
}
