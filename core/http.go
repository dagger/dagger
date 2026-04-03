package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/opencontainers/go-digest"
)

type ResolveHTTPRequestVersionOpts struct {
	URL      string
	Checksum dagql.Optional[dagql.String]
}

type ResolvedHTTPVersion struct {
	ETag         dagql.Optional[dagql.String]
	LastModified dagql.Optional[dagql.String]
	Digest       dagql.Optional[dagql.String]
}

type FetchHTTPRequestOpts struct {
	URL                 string
	Filename            string
	Permissions         int
	Checksum            dagql.Optional[dagql.String]
	AuthorizationHeader string

	ResolvedETag         dagql.Optional[dagql.String]
	ResolvedLastModified dagql.Optional[dagql.String]
	ResolvedDigest       dagql.Optional[dagql.String]
}

type HTTPFetchResult struct {
	File          *File
	ContentDigest digest.Digest
	LastModified  string
}

func ResolveHTTPVersion(
	ctx context.Context,
	query *Query,
	opts ResolveHTTPRequestVersionOpts,
) (*ResolvedHTTPVersion, error) {
	if opts.Checksum.Valid && opts.Checksum.Value != "" {
		return &ResolvedHTTPVersion{Digest: opts.Checksum}, nil
	}

	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, opts.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := doHTTPClientRequest(ctx, headReq)
	switch {
	case err == nil:
		defer resp.Body.Close()
		if etag := etagValue(resp.Header.Get("ETag")); etag != "" {
			return &ResolvedHTTPVersion{ETag: dagql.Opt(dagql.String(etag))}, nil
		}
		if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
			return &ResolvedHTTPVersion{LastModified: dagql.Opt(dagql.String(lastModified))}, nil
		}
	case err != nil && (resp == nil || resp.StatusCode != http.StatusMethodNotAllowed):
		return nil, err
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err = doHTTPClientRequest(ctx, getReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	h := sha256.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return nil, err
	}
	dgst := digest.NewDigest(digest.SHA256, h)
	return &ResolvedHTTPVersion{Digest: dagql.Opt(dagql.String(dgst.String()))}, nil
}

func FetchHTTPFile(
	ctx context.Context,
	query *Query,
	opts FetchHTTPRequestOpts,
) (_ *HTTPFetchResult, rerr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return nil, err
	}
	if opts.AuthorizationHeader != "" {
		req.Header.Set("Authorization", opts.AuthorizationHeader)
	}

	resp, err := doHTTPClientRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if opts.ResolvedETag.Valid {
		got := etagValue(resp.Header.Get("ETag"))
		if got != string(opts.ResolvedETag.Value) {
			return nil, fmt.Errorf("http ETag mismatch: expected %q, got %q", opts.ResolvedETag.Value, got)
		}
	}
	if opts.ResolvedLastModified.Valid {
		got := resp.Header.Get("Last-Modified")
		if got != string(opts.ResolvedLastModified.Value) {
			return nil, fmt.Errorf("http Last-Modified mismatch: expected %q, got %q", opts.ResolvedLastModified.Value, got)
		}
	}

	expectedChecksum, err := parseOptionalChecksum(opts.Checksum)
	if err != nil {
		return nil, err
	}
	resolvedDigest, err := parseOptionalChecksum(opts.ResolvedDigest)
	if err != nil {
		return nil, err
	}

	bkref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("http url %s", opts.URL)),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			_ = bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	h := sha256.New()
	err = MountRef(ctx, bkref, func(out string, _ *mount.Mount) error {
		dest := filepath.Join(out, opts.Filename)
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(opts.Permissions))
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}

		timestamp := time.Unix(0, 0)
		if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
			if parsed, err := http.ParseTime(lastModified); err == nil {
				timestamp = parsed
			}
		}
		return os.Chtimes(dest, timestamp, timestamp)
	})
	if err != nil {
		return nil, fmt.Errorf("file write failed: %w", err)
	}

	contentDigest := digest.NewDigest(digest.SHA256, h)
	if expectedChecksum != "" && contentDigest != expectedChecksum {
		return nil, fmt.Errorf("http checksum mismatch: expected %s, got %s", expectedChecksum, contentDigest)
	}
	if resolvedDigest != "" && contentDigest != resolvedDigest {
		return nil, fmt.Errorf("http content digest mismatch: expected %s, got %s", resolvedDigest, contentDigest)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil

	file, err := NewFileWithSnapshot(opts.Filename, query.Platform(), nil, snap)
	if err != nil {
		_ = snap.Release(context.WithoutCancel(ctx))
		return nil, err
	}

	return &HTTPFetchResult{
		File:          file,
		ContentDigest: contentDigest,
		LastModified:  resp.Header.Get("Last-Modified"),
	}, nil
}

func doHTTPClientRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	dns, err := DNSConfig(ctx)
	if err != nil {
		return nil, err
	}
	client := http.Client{
		Transport: netconfhttp.NewTransport(http.DefaultTransport, dns),
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if req.Method == http.MethodHead && resp.StatusCode == http.StatusMethodNotAllowed {
		return resp, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("invalid response status %s", resp.Status)
	}
	return resp, nil
}

func parseOptionalChecksum(raw dagql.Optional[dagql.String]) (digest.Digest, error) {
	if !raw.Valid || raw.Value == "" {
		return "", nil
	}
	return digest.Parse(string(raw.Value))
}

func etagValue(v string) string {
	return strings.TrimPrefix(v, "W/")
}
