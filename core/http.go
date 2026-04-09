package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

const httpStateCanonicalPath = "contents"
const httpStateCanonicalPermissions = 0o600

type HTTPState struct {
	URL string

	mu sync.Mutex

	ETag          string
	LastModified  string
	ContentDigest digest.Digest

	snapshot   bkcache.ImmutableRef
	snapshotID string
}

type persistedHTTPStatePayload struct {
	URL           string `json:"url"`
	ETag          string `json:"etag,omitempty"`
	LastModified  string `json:"lastModified,omitempty"`
	ContentDigest string `json:"contentDigest,omitempty"`
}

type FetchHTTPRequestOpts struct {
	URL                 string
	Filename            string
	Permissions         int
	Checksum            dagql.Optional[dagql.String]
	AuthorizationHeader string
}

type HTTPFetchResult struct {
	File          *File
	ContentDigest digest.Digest
	LastModified  string
}

var _ dagql.PersistedObject = (*HTTPState)(nil)
var _ dagql.PersistedObjectDecoder = (*HTTPState)(nil)
var _ dagql.OnReleaser = (*HTTPState)(nil)

func (*HTTPState) Type() *ast.Type {
	return &ast.Type{
		NamedType: "HTTPState",
		NonNull:   true,
	}
}

func (*HTTPState) TypeDescription() string {
	return "An internal persistent HTTP state."
}

func (state *HTTPState) OnRelease(ctx context.Context) error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.snapshot == nil {
		return nil
	}
	err := state.snapshot.Release(ctx)
	state.snapshot = nil
	return err
}

func (state *HTTPState) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	snapshotID := state.snapshotID
	if snapshotID == "" && state.snapshot != nil {
		snapshotID = state.snapshot.SnapshotID()
	}
	if snapshotID == "" {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{{
		RefKey: snapshotID,
		Role:   "snapshot",
		Slot:   "/",
	}}
}

func (state *HTTPState) CacheUsageMayChange() bool {
	return true
}

func (state *HTTPState) CacheUsageIdentities() []string {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.snapshot != nil {
		return []string{state.snapshot.SnapshotID()}
	}
	if state.snapshotID == "" {
		return nil
	}
	return []string{state.snapshotID}
}

func (state *HTTPState) CacheUsageSize(ctx context.Context, identity string) (int64, bool, error) {
	if state == nil {
		return 0, false, nil
	}
	state.mu.Lock()
	snapshot := state.snapshot
	snapshotID := state.snapshotID
	state.mu.Unlock()
	if snapshot != nil && snapshot.SnapshotID() != identity {
		return 0, false, nil
	}
	if snapshot == nil && (snapshotID == "" || snapshotID != identity) {
		return 0, false, nil
	}
	if snapshot != nil {
		size, err := snapshot.Size(ctx)
		if err != nil {
			return 0, false, err
		}
		return size, true, nil
	}
	if snapshotID == "" {
		return 0, false, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return 0, false, err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		_ = ref.Release(context.WithoutCancel(ctx))
	}()
	size, err := ref.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (state *HTTPState) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if state == nil {
		return nil, fmt.Errorf("encode persisted http state: nil state")
	}
	return json.Marshal(persistedHTTPStatePayload{
		URL:           state.URL,
		ETag:          state.ETag,
		LastModified:  state.LastModified,
		ContentDigest: state.ContentDigest.String(),
	})
}

func (*HTTPState) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedHTTPStatePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted http state payload: %w", err)
	}
	state := &HTTPState{
		URL:          persisted.URL,
		ETag:         persisted.ETag,
		LastModified: persisted.LastModified,
	}
	if persisted.ContentDigest != "" {
		dgst, err := digest.Parse(persisted.ContentDigest)
		if err != nil {
			return nil, fmt.Errorf("decode persisted http state content digest: %w", err)
		}
		state.ContentDigest = dgst
	}
	if resultID != 0 {
		links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "http state")
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if link.Role != "snapshot" {
				continue
			}
			state.snapshotID = link.RefKey
			break
		}
	}
	return state, nil
}

func (state *HTTPState) Resolve(
	ctx context.Context,
	query *Query,
	checksum dagql.Optional[dagql.String],
	permissions int,
	name string,
) (_ *HTTPFetchResult, rerr error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	expectedChecksum, err := parseOptionalChecksum(checksum)
	if err != nil {
		return nil, fmt.Errorf("invalid checksum %q: %w", checksum.Value, err)
	}

	if state.snapshot == nil && state.snapshotID != "" {
		snapshot, err := query.SnapshotManager().GetBySnapshotID(ctx, state.snapshotID, bkcache.NoUpdateLastUsed)
		if err != nil {
			return nil, fmt.Errorf("reopen http state snapshot %q: %w", state.snapshotID, err)
		}
		state.snapshot = snapshot
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, state.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if state.ETag != "" {
		req.Header.Set("If-None-Match", state.ETag)
	} else if state.LastModified != "" {
		req.Header.Set("If-Modified-Since", state.LastModified)
	}

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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
		return nil, fmt.Errorf("invalid response status %s", resp.Status)
	}

	if resp.StatusCode == http.StatusNotModified {
		if state.snapshot == nil {
			return nil, fmt.Errorf("http state %q returned 304 without a cached snapshot", state.URL)
		}
		if etag := etagValue(resp.Header.Get("ETag")); etag != "" {
			state.ETag = etag
		}
		if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
			state.LastModified = lastModified
		}
		if expectedChecksum != "" && state.ContentDigest != expectedChecksum {
			return nil, fmt.Errorf("http checksum mismatch: expected %s, got %s", expectedChecksum, state.ContentDigest)
		}
		return state.fileResult(ctx, query, name, permissions)
	}

	newCanonical, newDigest, newLastModified, newETag, err := writeHTTPStateSnapshot(ctx, query, state.URL, resp)
	if err != nil {
		return nil, err
	}

	if expectedChecksum != "" && newDigest != expectedChecksum {
		_ = newCanonical.Release(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("http checksum mismatch: expected %s, got %s", expectedChecksum, newDigest)
	}

	if state.ContentDigest == "" || newDigest != state.ContentDigest {
		if state.snapshot != nil {
			_ = state.snapshot.Release(context.WithoutCancel(ctx))
		}
		state.snapshot = newCanonical
		state.snapshotID = newCanonical.SnapshotID()
		state.ContentDigest = newDigest
	} else {
		_ = newCanonical.Release(context.WithoutCancel(ctx))
	}
	state.ETag = newETag
	state.LastModified = newLastModified

	return state.fileResult(ctx, query, name, permissions)
}

func writeHTTPStateSnapshot(
	ctx context.Context,
	query *Query,
	url string,
	resp *http.Response,
) (_ bkcache.ImmutableRef, _ digest.Digest, _ string, _ string, rerr error) {
	bkref, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("http state %s", url)),
	)
	if err != nil {
		return nil, "", "", "", err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			_ = bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	h := sha256.New()
	lastModified := resp.Header.Get("Last-Modified")
	err = MountRef(ctx, bkref, func(out string, _ *mount.Mount) error {
		dest := filepath.Join(out, httpStateCanonicalPath)
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(httpStateCanonicalPermissions))
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
		if lastModified != "" {
			if parsed, err := http.ParseTime(lastModified); err == nil {
				timestamp = parsed
			}
		}
		return os.Chtimes(dest, timestamp, timestamp)
	})
	if err != nil {
		return nil, "", "", "", fmt.Errorf("file write failed: %w", err)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, "", "", "", err
	}
	bkref = nil
	return snap, digest.NewDigest(digest.SHA256, h), lastModified, etagValue(resp.Header.Get("ETag")), nil
}

func (state *HTTPState) fileResult(
	ctx context.Context,
	query *Query,
	name string,
	permissions int,
) (*HTTPFetchResult, error) {
	if state.snapshot == nil {
		return nil, fmt.Errorf("http state %q has no snapshot", state.URL)
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		state.snapshot,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("http state resolve %s", name)),
	)
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		src, err := RootPathWithoutFinalSymlink(root, httpStateCanonicalPath)
		if err != nil {
			return err
		}
		dst, err := RootPathWithoutFinalSymlink(root, name)
		if err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return TrimErrPathPrefix(err, root)
		}
		if err := os.Chmod(dst, os.FileMode(permissions)); err != nil {
			return TrimErrPathPrefix(err, root)
		}
		return nil
	})
	if err != nil {
		_ = newRef.Release(context.WithoutCancel(ctx))
		return nil, err
	}
	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	file := &File{
		Platform: query.Platform(),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	file.File.setValue(name)
	file.Snapshot.setValue(snap)
	return &HTTPFetchResult{
		File:          file,
		ContentDigest: state.ContentDigest,
		LastModified:  state.LastModified,
	}, nil
}

func FetchHTTPFile(
	ctx context.Context,
	query *Query,
	opts FetchHTTPRequestOpts,
) (_ *HTTPFetchResult, rerr error) {
	expectedChecksum, err := parseOptionalChecksum(opts.Checksum)
	if err != nil {
		return nil, fmt.Errorf("invalid checksum %q: %w", opts.Checksum.Value, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if opts.AuthorizationHeader != "" {
		req.Header.Set("Authorization", opts.AuthorizationHeader)
	}

	resp, err := doHTTPClientRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil

	file := &File{
		Platform: query.Platform(),
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	file.File.setValue(opts.Filename)
	file.Snapshot.setValue(snap)

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
