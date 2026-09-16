package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

const httpStateCanonicalPath = "contents"
const httpStateCanonicalPermissions = 0o600

type HTTPState struct {
	foreignUninitialized bool
	URL                  string

	mu sync.Mutex

	ETag          string
	LastModified  string
	ContentDigest digest.Digest

	snapshot   bkcache.ImmutableRef
	snapshotID string
}

type persistedHTTPStatePayload struct {
	Form          string `json:"form"`
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

func (state *HTTPState) CacheUsageSize(ctx context.Context, sizeProvider dagql.CacheUsageSizeProvider, identity string) (int64, bool, error) {
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
	if sizeProvider == nil {
		return 0, false, nil
	}
	size, err := sizeProvider.SnapshotSize(ctx, snapshotID)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (state *HTTPState) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = enc
	if state == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted http state: nil state")
	}
	state.mu.Lock()
	snapshotID := state.snapshotID
	if snapshotID == "" && state.snapshot != nil {
		snapshotID = state.snapshot.SnapshotID()
	}
	saved := persistedHTTPStatePayload{URL: state.URL, ETag: state.ETag, LastModified: state.LastModified, ContentDigest: state.ContentDigest.String()}
	saved.Form = persistedBackingForm(state.foreignUninitialized, snapshotID != "")
	state.mu.Unlock()
	var links []dagql.PersistedSnapshotRefLink
	if snapshotID != "" {
		links = []dagql.PersistedSnapshotRefLink{{
			RefKey: snapshotID,
			Role:   "snapshot",
		}}
	}
	payload, err := json.Marshal(saved)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return dagql.PersistedObjectEncoding{
		JSON:          payload,
		SnapshotLinks: links,
	}, nil
}

func (*HTTPState) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedHTTPStatePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted http state payload: %w", err)
	}
	state := &HTTPState{
		foreignUninitialized: persisted.Form == foreignUninitialized,
		URL:                  persisted.URL,
		ETag:                 persisted.ETag,
		LastModified:         persisted.LastModified,
	}
	if persisted.ContentDigest != "" {
		dgst, err := digest.Parse(persisted.ContentDigest)
		if err != nil {
			return nil, fmt.Errorf("decode persisted http state content digest: %w", err)
		}
		state.ContentDigest = dgst
	}
	if dec.ResultID() != 0 {
		links, err := loadPersistedSnapshotLinksByResultID(ctx, dec, "http state")
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

	// Encapsulated like the resolver's "pulling" span: hidden unless it
	// fails, surfacing as a labeled progress row only when bytes actually
	// move (a 304 revalidation emits no progress).
	span, ctx := tracing.StartSpan(ctx, "fetching "+state.URL, telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()

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
	// close through a closure: resp.Body is replaced with a progress reader
	// below, and `defer resp.Body.Close()` would capture the original body
	// at registration, leaving the wrapper (and its final progress emit on
	// early returns) unclosed
	defer func() {
		_ = resp.Body.Close()
	}()
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

	resp.Body = bkcache.NewProgressReader(ctx, state.URL, resp.ContentLength, resp.Body)
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
) (_ *HTTPFetchResult, rerr error) {
	if state.snapshot == nil {
		return nil, fmt.Errorf("http state %q has no snapshot", state.URL)
	}
	file, err := httpFileFromSnapshot(ctx, query, state.snapshot, name, permissions, query.Platform())
	if err != nil {
		return nil, err
	}
	return &HTTPFetchResult{File: file, ContentDigest: state.ContentDigest, LastModified: state.LastModified}, nil
}

// httpFileFromSnapshot derives a named output while the caller owns snapshot.
// It preserves the canonical file's timestamp and applies the selected mode.
func httpFileFromSnapshot(ctx context.Context, query *Query, snapshot bkcache.ImmutableRef, name string, permissions int, platform Platform) (_ *File, rerr error) {
	newRef, err := query.SnapshotManager().New(
		ctx,
		snapshot,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("http state resolve %s", name)),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if newRef != nil {
			rerr = errors.Join(rerr, newRef.Release(context.WithoutCancel(ctx)))
		}
	}()
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
		return nil, err
	}
	snap, err := newRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	newRef = nil
	file := &File{
		Platform: platform,
		File:     new(LazyAccessor[string, *File]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
	}
	file.SetPath(name)
	file.SetSnapshot(snap)
	return file, nil
}

func FetchHTTPFile(ctx context.Context, query *Query, opts FetchHTTPRequestOpts) (*HTTPFetchResult, error) {
	return fetchHTTPFile(ctx, query, opts, false)
}

func fetchHTTPFile(ctx context.Context, query *Query, opts FetchHTTPRequestOpts, useFileResultLayout bool) (_ *HTTPFetchResult, rerr error) {
	span, ctx := tracing.StartSpan(ctx, "fetching "+opts.URL, telemetry.Encapsulated(), telemetry.Encapsulate())
	defer func() {
		tracing.FinishWithError(span, rerr)
	}()

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
	resp.Body = bkcache.NewProgressReader(ctx, opts.URL, resp.ContentLength, resp.Body)
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
	err = MountRef(ctx, bkref, func(out string, _ *mount.Mount) (writeErr error) {
		defer func() {
			if useFileResultLayout && writeErr != nil {
				writeErr = TrimErrPathPrefix(writeErr, out)
			}
		}()
		dest := filepath.Join(out, opts.Filename)
		if useFileResultLayout {
			var err error
			dest, err = RootPathWithoutFinalSymlink(out, opts.Filename)
			if err != nil {
				return err
			}
		}
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

		if useFileResultLayout {
			if err := os.Chmod(dest, os.FileMode(opts.Permissions)); err != nil {
				return err
			}
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
	file.SetPath(opts.Filename)
	file.SetSnapshot(snap)

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

const persistedFileLazyKindHTTPResolve = "httpResolve"

type FileHTTPResolveLazy struct {
	LazyState
	URL         string
	Filename    string
	Permissions int
	Checksum    dagql.Optional[dagql.String]
	BodyDigest  digest.Digest
}
type persistedFileHTTPResolveLazy struct {
	URL         string  `json:"url"`
	Filename    string  `json:"filename"`
	Permissions int     `json:"permissions"`
	Checksum    *string `json:"checksum"`
	BodyDigest  string  `json:"bodyDigest"`
}

func (p *persistedFileHTTPResolveLazy) validate() error {
	dgst := digest.Digest(p.BodyDigest)
	if err := dgst.Validate(); err != nil {
		return fmt.Errorf("HTTP File operation body digest: %w", err)
	}
	if dgst.Algorithm() != digest.SHA256 {
		return fmt.Errorf("HTTP File operation body digest must be SHA-256")
	}
	return nil
}
func (lazy *FileHTTPResolveLazy) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	p := persistedFileHTTPResolveLazy{URL: lazy.URL, Filename: lazy.Filename, Permissions: lazy.Permissions, BodyDigest: lazy.BodyDigest.String()}
	if lazy.Checksum.Valid {
		value := string(lazy.Checksum.Value)
		p.Checksum = &value
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
func decodeFileHTTPResolveLazy(payload json.RawMessage) (Lazy[*File], error) {
	var p persistedFileHTTPResolveLazy
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("decode HTTP File operation: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	lazy := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: p.URL, Filename: p.Filename, Permissions: p.Permissions, BodyDigest: digest.Digest(p.BodyDigest)}
	if p.Checksum != nil {
		lazy.Checksum = dagql.Optional[dagql.String]{Valid: true, Value: dagql.String(*p.Checksum)}
	}
	return lazy, nil
}
func (*FileHTTPResolveLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

type HTTPBodyDigestMismatchError struct {
	Recorded digest.Digest
	Fetched  digest.Digest
}

func (err *HTTPBodyDigestMismatchError) Error() string {
	return fmt.Sprintf("HTTP File operation body mismatch: recorded %s, fetched %s", err.Recorded, err.Fetched)
}

// pinHTTPBody acquires independent ownership before releasing the state lock.
// A changed or unavailable body is a miss; no validators are read or changed.
func (state *HTTPState) pinHTTPBody(ctx context.Context, manager bkcache.SnapshotManager, bodyDigest digest.Digest) (bkcache.ImmutableRef, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if state.foreignUninitialized || state.ContentDigest != bodyDigest {
		return nil, nil
	}
	id := state.snapshotID
	if id == "" && state.snapshot != nil {
		id = state.snapshot.SnapshotID()
	}
	if id == "" {
		return nil, nil
	}
	ref, err := manager.PinSnapshot(ctx, id)
	if httpSnapshotUnavailable(err) {
		return nil, nil
	}
	return ref, err
}

func httpSnapshotUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if !httpSnapshotUnavailable(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return httpSnapshotUnavailable(wrapped.Unwrap())
	}
	return bkcache.IsNotFound(err) || cerrdefs.IsNotFound(err)
}

func (lazy *FileHTTPResolveLazy) Evaluate(ctx context.Context, file *File) error {
	return file.evaluateLazy(ctx, &lazy.LazyState, "Query.__httpFile", func(ctx context.Context) (rerr error) {
		if err := validateProducedFileReceiver(file); err != nil {
			return err
		}
		if err := (&persistedFileHTTPResolveLazy{BodyDigest: lazy.BodyDigest.String()}).validate(); err != nil {
			return err
		}
		checksum, err := parseOptionalChecksum(lazy.Checksum)
		if err != nil {
			return fmt.Errorf("invalid checksum %q: %w", lazy.Checksum.Value, err)
		}
		if checksum != "" && checksum != lazy.BodyDigest {
			return fmt.Errorf("http checksum mismatch: expected %s, recorded %s", checksum, lazy.BodyDigest)
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
		}
		var state dagql.ObjectResult[*HTTPState]
		if err := srv.Select(ctx, srv.Root(), &state, dagql.Selector{Field: "_httpState", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(lazy.URL)}}}); err != nil {
			return err
		}
		canonical, err := state.Self().pinHTTPBody(ctx, query.SnapshotManager(), lazy.BodyDigest)
		if err != nil {
			return err
		}
		var candidate *File
		defer func() {
			cleanup := context.WithoutCancel(ctx)
			if candidate != nil {
				rerr = errors.Join(rerr, candidate.OnRelease(cleanup))
			}
			if canonical != nil {
				rerr = errors.Join(rerr, canonical.Release(cleanup))
			}
		}()
		if canonical != nil {
			candidate, err = httpFileFromSnapshot(ctx, query, canonical, lazy.Filename, lazy.Permissions, file.Platform)
			if err != nil {
				return err
			}
		} else {
			fetched, err := fetchHTTPFile(ctx, query, FetchHTTPRequestOpts{URL: lazy.URL, Filename: lazy.Filename, Permissions: lazy.Permissions, Checksum: lazy.Checksum}, true)
			if err != nil {
				return err
			}
			candidate = fetched.File
			if fetched.ContentDigest != lazy.BodyDigest {
				return &HTTPBodyDigestMismatchError{Recorded: lazy.BodyDigest, Fetched: fetched.ContentDigest}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return moveProducedFile(file, candidate)
	})
}
