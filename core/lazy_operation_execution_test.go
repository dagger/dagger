package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/moby/locker"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
)

type operationExecutionServer struct {
	*cacheVolumeTestQueryServer
	srv     *dagql.Server
	store   content.Store
	builtin content.Store
	locker  *locker.Locker
}

func (s *operationExecutionServer) Locker() *locker.Locker                        { return s.locker }
func (s *operationExecutionServer) DNS() *oci.DNSConfig                           { return &oci.DNSConfig{} }
func (s *operationExecutionServer) Server(context.Context) (*dagql.Server, error) { return s.srv, nil }
func (s *operationExecutionServer) OCIStore() content.Store                       { return s.store }
func (s *operationExecutionServer) BuiltinOCIStore() content.Store                { return s.builtin }
func (s *operationExecutionServer) Platform() Platform {
	return Platform{OS: "linux", Architecture: "amd64"}
}

// foldedIntoNative marks a test whose production path mounts read-only or
// enters a mount namespace, which no unit test may need. Its rows are owed by
// the named case in core/integration, and the test is deleted when that case
// lands. The skip is unconditional: it does not depend on who runs the test.
func foldedIntoNative(t *testing.T, native string) {
	t.Helper()
	t.Skipf("folded into native %s; deleted when that case lands", native)
}
type executionFixtureValues struct {
	ctx    context.Context
	store  *testutil.Store
	cache  *dagql.Cache
	srv    *dagql.Server
	server *operationExecutionServer
}

func newExecutionFixture(t *testing.T) executionFixtureValues {
	t.Helper()
	store := testutil.NewStore(t)
	ctx, cache, srv := transferCache(t, store, "", "operation-execution")
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	server := &operationExecutionServer{cacheVolumeTestQueryServer: &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: store.Manager}, srv: srv, store: store.Content}
	server.locker = locker.New()
	query.Server = server
	srv.InstallObject(dagql.NewClass[*HTTPState](srv))
	dagql.Fields[*Query]{dagql.Func("_httpState", func(_ context.Context, _ *Query, args struct{ URL string }) (*HTTPState, error) {
		return &HTTPState{URL: args.URL}, nil
	}).IsPersistable()}.Install(srv)
	return executionFixtureValues{ctx: ctx, store: store, cache: cache, srv: srv, server: server}
}

func executionFixture(t *testing.T) (context.Context, *testutil.Store, *dagql.Cache, *dagql.Server, *operationExecutionServer) {
	t.Helper()
	f := newExecutionFixture(t)
	return f.ctx, f.store, f.cache, f.srv, f.server
}

// executionContext is the fixture for tests that use only its context.
func executionContext(t *testing.T) context.Context {
	t.Helper()
	return newExecutionFixture(t).ctx
}

// inUmaskChild lets a subtest run under a umask without changing the umask of
// the test process, which every other test shares. In the test process it
// re-executes the test binary for just this subtest, with a deadline, requires
// the child to pass and returns false. In that child it sets the umask, which
// dies with the process, and returns true, and the caller runs its body.
func inUmaskChild(t *testing.T, mask int) bool {
	t.Helper()
	const marker = "DAGGER_TEST_UMASK_CHILD"
	const ran = "umask child ran its body"
	if os.Getenv(marker) == t.Name() {
		syscall.Umask(mask)
		// A pattern that matched no test would also exit zero.
		t.Cleanup(func() { fmt.Println(ran) })
		return true
	}
	var pattern []string
	for _, name := range strings.Split(t.Name(), "/") {
		pattern = append(pattern, "^"+regexp.QuoteMeta(name)+"$")
	}
	// The child's own timeout fires first, so a hung child prints its stacks
	// before this deadline kills it.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run="+strings.Join(pattern, "/"), "-test.count=1", "-test.timeout=20s")
	child.Env = append(os.Environ(), marker+"="+t.Name())
	output, err := child.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), ran, "%s", output)
	return false
}
func freshLazyOperationFile() *File {
	return &File{Platform: Platform{OS: "linux", Architecture: "arm64"}, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
}
func decodedHTTPLazyOperation(t *testing.T, ctx context.Context, lazy *FileHTTPResolveLazy) *FileHTTPResolveLazy {
	t.Helper()
	raw, err := lazy.EncodePersisted(ctx, nil)
	require.NoError(t, err)
	decoded, err := decodeFileHTTPResolveLazy(raw)
	require.NoError(t, err)
	return decoded.(*FileHTTPResolveLazy)
}
func producedFileContents(t *testing.T, file *File) ([]byte, os.FileInfo) {
	t.Helper()
	name, snapshot, err := fileOutput(file)
	require.NoError(t, err)
	path, err := RootPathWithoutFinalSymlink(testutil.Root(t, snapshot), name)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	var data []byte
	// Only root can read a file whose mode grants no read permission, so a
	// mode-0 file is checked by its mode and time and not by its bytes.
	if info.Mode().Perm()&0400 != 0 {
		data, err = os.ReadFile(path)
		require.NoError(t, err)
	}
	return data, info
}
func TestHTTPLazyOperationEvaluate(t *testing.T) {
	ctx := executionContext(t)
	for _, test := range []struct {
		name, body, lastModified, checksum string
		valid                              bool
		status                             int
		wantErr                            string
	}{
		{name: "equal", body: "saved", status: 200}, {name: "changed timestamp", body: "saved", lastModified: "Wed, 21 Oct 2015 07:28:00 GMT", status: 200},
		{name: "invalid timestamp", body: "saved", lastModified: "invalid", status: 200}, {name: "empty checksum", body: "saved", valid: true, status: 200},
		{name: "checksum", body: "saved", checksum: digest.FromString("saved").String(), valid: true, status: 200},
		{name: "changed body", body: "changed", status: 200, wantErr: "body mismatch"}, {name: "checksum mismatch", body: "changed", checksum: digest.FromString("saved").String(), valid: true, status: 200, wantErr: "checksum mismatch"},
		{name: "bad checksum", body: "saved", checksum: "invalid", valid: true, status: 200, wantErr: "invalid checksum"},
		{name: "status", body: "saved", status: 500, wantErr: "invalid response status"}, {name: "partial", body: "saved", status: 206},
		{name: "not modified", status: 304, wantErr: "body mismatch"}, {name: "no content", status: 204, wantErr: "body mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := atomic.Int64{}
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Empty(t, r.Header.Get("If-None-Match"))
				require.Empty(t, r.Header.Get("If-Modified-Since"))
				require.Empty(t, r.Header.Get("Authorization"))
				require.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				w.Header().Set("Last-Modified", test.lastModified)
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer origin.Close()
			lazy := decodedHTTPLazyOperation(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "data", Permissions: 0644, Checksum: dagql.Optional[dagql.String]{Valid: test.valid, Value: dagql.String(test.checksum)}, BodyDigest: digest.FromString("saved")})
			file := freshLazyOperationFile()
			err := lazy.Evaluate(ctx, file)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				_, ready := file.Snapshot.Peek()
				require.False(t, ready)
			} else {
				require.NoError(t, err)
				data, _ := producedFileContents(t, file)
				require.Equal(t, "saved", string(data))
				require.Equal(t, "arm64", file.Platform.Architecture)
				require.NoError(t, file.OnRelease(ctx))
			}
			wantCalls := int64(1)
			if test.name == "bad checksum" {
				wantCalls = 0
			}
			require.Equal(t, wantCalls, calls.Load())
		})
	}
	for _, status := range []int{204, 304} {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		lazy := decodedHTTPLazyOperation(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "empty", BodyDigest: digest.FromString("")})
		file := freshLazyOperationFile()
		require.NoError(t, lazy.Evaluate(ctx, file))
		require.NoError(t, file.OnRelease(ctx))
		origin.Close()
	}
	t.Run("transport", func(t *testing.T) {
		lazy := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "http://127.0.0.1:1", Filename: "data", BodyDigest: digest.FromString("saved")}
		file := freshLazyOperationFile()
		require.Error(t, lazy.Evaluate(ctx, file))
		_, ready := file.Snapshot.Peek()
		require.False(t, ready)
	})
	t.Run("canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		lazy := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "http://127.0.0.1:1", Filename: "data", BodyDigest: digest.FromString("saved")}
		require.ErrorIs(t, lazy.Evaluate(cctx, freshLazyOperationFile()), context.Canceled)
	})
}

func TestHTTPLazyOperationWriter(t *testing.T) {
	ctx := executionContext(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	const modified = "Wed, 21 Oct 2015 07:28:00 GMT"
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", modified)
		fmt.Fprint(w, "saved")
	}))
	defer origin.Close()
	for _, name := range []string{"data.txt", "/data.txt", "../data.txt", "a/../data.txt", "./data.txt", "sub/data.txt"} {
		for _, mode := range []int{0, 0600, 0644, 0755} {
			t.Run(fmt.Sprintf("%s/%o", name, mode), func(t *testing.T) {
				state := &HTTPState{URL: origin.URL}
				defer state.OnRelease(ctx)
				eager, eagerErr := state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, mode, name)
				output := freshLazyOperationFile()
				operation := decodedHTTPLazyOperation(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: name, Permissions: mode, BodyDigest: digest.FromString("saved")})
				restoreErr := operation.Evaluate(ctx, output)
				if name == "sub/data.txt" {
					require.Error(t, eagerErr)
					require.Error(t, restoreErr)
					var a *os.LinkError
					var b *os.PathError
					require.ErrorAs(t, eagerErr, &a)
					require.ErrorAs(t, restoreErr, &b)
					require.Equal(t, a.Err, b.Err)
					require.Equal(t, a.New, b.Path)
					return
				}
				require.NoError(t, eagerErr)
				require.NoError(t, restoreErr)
				a, ai := producedFileContents(t, eager.File)
				b, bi := producedFileContents(t, output)
				require.Equal(t, a, b)
				require.Equal(t, os.FileMode(mode), bi.Mode().Perm())
				require.Equal(t, ai.Mode(), bi.Mode())
				require.Equal(t, ai.ModTime(), bi.ModTime())
				path, _ := output.File.Peek()
				require.Equal(t, name, path)
				require.NoError(t, eager.File.OnRelease(ctx))
				require.NoError(t, output.OnRelease(ctx))
			})
		}
	}
	t.Run("restrictive umask", func(t *testing.T) {
		if !inUmaskChild(t, 0077) {
			return
		}
		output := freshLazyOperationFile()
		operation := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: origin.URL, Filename: "data", Permissions: 0755, BodyDigest: digest.FromString("saved")}
		require.NoError(t, operation.Evaluate(ctx, output))
		defer output.OnRelease(ctx)
		_, info := producedFileContents(t, output)
		require.EqualValues(t, 0755, info.Mode().Perm())
	})
	t.Run("public eager layout", func(t *testing.T) {
		if !inUmaskChild(t, 0) {
			return
		}
		for _, name := range []string{"data", "/data", "../data", "a/../data", "./data"} {
			output, err := FetchHTTPFile(ctx, query, FetchHTTPRequestOpts{URL: origin.URL, Filename: name, Permissions: 0644, AuthorizationHeader: "saved-auth"})
			require.NoError(t, err)
			require.Nil(t, output.File.Lazy)
			require.Empty(t, output.File.lazyJSON)
			path, snapshot, err := fileOutput(output.File)
			require.NoError(t, err)
			require.Equal(t, name, path)
			data, err := os.ReadFile(filepath.Join(testutil.Root(t, snapshot), "data"))
			if name == "../data" {
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.NoError(t, err)
				require.Equal(t, "saved", string(data))
			}
			require.NoError(t, output.File.OnRelease(ctx))
		}
	})
}

type operationBodyTransport struct {
	http.RoundTripper
	closed *atomic.Int64
	onEOF  func()
}

func (t *operationBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err == nil {
		resp.Body = &operationObservedBody{ReadCloser: resp.Body, closed: t.closed, onEOF: t.onEOF}
	}
	return resp, err
}

type operationObservedBody struct {
	io.ReadCloser
	closed           *atomic.Int64
	onEOF            func()
	done, pendingEOF bool
}

func (b *operationObservedBody) Read(p []byte) (int, error) {
	if b.pendingEOF {
		b.pendingEOF = false
		if b.onEOF != nil {
			b.onEOF()
		}
		b.done = true
		return 0, io.EOF
	}
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF && !b.done {
		if n > 0 {
			b.pendingEOF = true
			return n, nil
		}
		b.done = true
		if b.onEOF != nil {
			b.onEOF()
		}
	}
	return n, err
}
func (b *operationObservedBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

type operationObservedMount struct {
	bkcache.MountableRef
	root *string
}

func (m *operationObservedMount) Mount() ([]mount.Mount, func() error, error) {
	mounts, release, err := m.MountableRef.Mount()
	if err == nil && len(mounts) > 0 {
		*m.root = mounts[0].Source
	}
	return mounts, release, err
}

type observedLazyOperationManager struct {
	bkcache.SnapshotManager
	mutableReleases, immutableReleases atomic.Int64
	commitErr                          error
	mountErr                           error
	writerRoot                         string
}

func (m *observedLazyOperationManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	if p, ok := parent.(*observedLazyOperationSnapshot); ok {
		parent = p.ImmutableRef
	}
	ref, err := m.SnapshotManager.New(ctx, parent, opts...)
	if err != nil {
		return nil, err
	}
	return &observedLazyOperationMutable{MutableRef: ref, manager: m}, nil
}

type observedLazyOperationMutable struct {
	bkcache.MutableRef
	manager *observedLazyOperationManager
}

func (r *observedLazyOperationMutable) Release(ctx context.Context) error {
	r.manager.mutableReleases.Add(1)
	return r.MutableRef.Release(ctx)
}
func (r *observedLazyOperationMutable) Mount(ctx context.Context, ro bool) (bkcache.MountableRef, error) {
	if r.manager.mountErr != nil {
		return nil, r.manager.mountErr
	}
	ref, err := r.MutableRef.Mount(ctx, ro)
	if err != nil {
		return nil, err
	}
	return &operationObservedMount{MountableRef: ref, root: &r.manager.writerRoot}, nil
}
func (r *observedLazyOperationMutable) Commit(ctx context.Context) (bkcache.ImmutableRef, error) {
	if r.manager.commitErr != nil {
		return nil, r.manager.commitErr
	}
	ref, err := r.MutableRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return &observedLazyOperationSnapshot{ImmutableRef: ref, manager: r.manager}, nil
}

type observedLazyOperationSnapshot struct {
	bkcache.ImmutableRef
	manager *observedLazyOperationManager
}

func (r *observedLazyOperationSnapshot) Release(ctx context.Context) error {
	r.manager.immutableReleases.Add(1)
	return r.ImmutableRef.Release(ctx)
}

func TestHTTPLazyOperationCleanup(t *testing.T) {
	ctx, store, _, _, server := executionFixture(t)
	for _, exit := range []string{"copy", "close", "chmod", "timestamp", "checksum", "commit", "mount", "digest", "move", "success"} {
		t.Run(exit, func(t *testing.T) {
			if exit == "timestamp" && os.Getenv("DAGGER_TEST_HTTP_TIMESTAMP_FAULT") != "1" {
				tracer, err := exec.LookPath("strace")
				if err != nil {
					// The timestamp fault is injected by tracing the re-executed
					// test binary's utimensat; without strace there is no way to
					// inject it, so the case is unexecuted rather than failed.
					t.Skipf("the timestamp fault needs strace to inject it: %v", err)
				}
				binary, err := os.Executable()
				require.NoError(t, err)
				cmd := exec.Command(tracer, "-f", "-qq", "-o", filepath.Join(t.TempDir(), "syscalls.log"), "-e", "trace=utimensat", "-e", "inject=utimensat:error=EIO:when=1", binary, "-test.run", "^TestHTTPLazyOperationCleanup/timestamp$", "-test.count=1", "-test.v")
				cmd.Env = append(os.Environ(), "DAGGER_TEST_HTTP_TIMESTAMP_FAULT=1")
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))
				return
			}
			manager := &observedLazyOperationManager{SnapshotManager: store.Manager}
			server.cacheManager = manager
			defer func() { server.cacheManager = store.Manager }()
			if exit == "commit" {
				manager.commitErr = errors.New("injected commit")
			}
			if exit == "mount" {
				manager.mountErr = errors.New("injected mount")
			}
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if exit == "copy" {
					w.Header().Set("Content-Length", "100")
				}
				fmt.Fprint(w, "body")
			}))
			defer origin.Close()
			operation := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: origin.URL, Filename: "data", Permissions: 0644, BodyDigest: digest.FromString("body")}
			if exit == "digest" {
				operation.BodyDigest = digest.FromString("other")
			}
			if exit == "checksum" {
				operation.Checksum = dagql.Optional[dagql.String]{Valid: true, Value: dagql.String(digest.FromString("other"))}
			}
			output := freshLazyOperationFile()
			closed := atomic.Int64{}
			previousTransport := http.DefaultTransport
			transport := &operationBodyTransport{RoundTripper: previousTransport, closed: &closed}
			transport.onEOF = func() {
				path := filepath.Join(manager.writerRoot, "data")
				switch exit {
				case "close":
					fds, err := os.ReadDir("/proc/self/fd")
					require.NoError(t, err)
					found := false
					for _, fd := range fds {
						target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
						if err == nil && target == path {
							number, err := strconv.Atoi(fd.Name())
							require.NoError(t, err)
							require.NoError(t, syscall.Close(number))
							found = true
							break
						}
					}
					require.True(t, found, "writer fd not found")
				case "chmod":
					require.NoError(t, os.Remove(path))
				case "move":
					output.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: "injected"})
				}
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = previousTransport }()
			err := operation.Evaluate(ctx, output)
			if exit == "checksum" {
				require.Zero(t, closed.Load())
			} else {
				require.EqualValues(t, 1, closed.Load())
			}
			if exit == "close" || exit == "chmod" {
				require.ErrorContains(t, err, exit)
			}
			if exit == "timestamp" {
				require.ErrorContains(t, err, "chtimes")
			}

			switch exit {
			case "checksum":
				require.ErrorContains(t, err, "checksum mismatch")
				require.Zero(t, manager.mutableReleases.Load())
				require.Zero(t, manager.immutableReleases.Load())
			case "success":
				require.NoError(t, err)
				require.Zero(t, manager.mutableReleases.Load())
				require.Zero(t, manager.immutableReleases.Load())
				require.NoError(t, output.OnRelease(ctx))
				require.EqualValues(t, 1, manager.immutableReleases.Load())
			case "digest", "move":
				require.Error(t, err)
				require.Zero(t, manager.mutableReleases.Load())
				require.EqualValues(t, 1, manager.immutableReleases.Load())
			default:
				require.Error(t, err)
				require.EqualValues(t, 1, manager.mutableReleases.Load())
				require.Zero(t, manager.immutableReleases.Load())
			}
			if exit != "success" && exit != "move" {
				_, ready := output.Snapshot.Peek()
				require.False(t, ready)
			}
		})
	}
}

func TestHTTPStateConcurrentResolveCapture(t *testing.T) {
	ctx := executionContext(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	entered := make(chan struct{})
	proceed := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-proceed
		w.Header().Set("ETag", "fresh")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		fmt.Fprint(w, "fresh")
	}))
	defer origin.Close()
	state := &HTTPState{URL: origin.URL}
	defer state.OnRelease(ctx)
	resolved := make(chan error, 1)
	go func() {
		out, err := state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, 0644, "data")
		if out != nil {
			err = errors.Join(err, out.File.OnRelease(ctx))
		}
		resolved <- err
	}()
	<-entered
	captured := make(chan dagql.PersistedObjectEncoding, 1)
	captureErr := make(chan error, 1)
	go func() { out, err := state.EncodePersistedObject(ctx, nil); captured <- out; captureErr <- err }()
	select {
	case <-captured:
		t.Fatal("capture passed Resolve's tuple lock")
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	require.NoError(t, <-resolved)
	out := <-captured
	require.NoError(t, <-captureErr)
	var payload persistedHTTPStatePayload
	require.NoError(t, json.Unmarshal(out.JSON, &payload))
	require.Equal(t, "fresh", payload.ETag)
	require.Equal(t, digest.FromString("fresh").String(), payload.ContentDigest)
	require.Equal(t, state.snapshotID, out.SnapshotLinks[0].RefKey)
}

func TestStatelessHTTPLazyOperationIsolation(t *testing.T) {
	ctx, store, cache, srv, server := executionFixture(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	plain := atomic.Int64{}
	conditional := atomic.Int64{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		if r.Header.Get("If-None-Match") != "" {
			conditional.Add(1)
			w.Header().Set("ETag", "new")
			fmt.Fprint(w, "new")
		} else {
			plain.Add(1)
			w.Header().Set("ETag", "old")
			fmt.Fprint(w, "old")
		}
	}))
	defer origin.Close()
	state := &HTTPState{URL: origin.URL}
	defer state.OnRelease(ctx)
	old, err := state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, 0644, "old")
	require.NoError(t, err)
	defer old.File.OnRelease(ctx)
	operation := decodedHTTPLazyOperation(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "old", Permissions: 0644, BodyDigest: old.ContentDigest})
	output := freshLazyOperationFile()
	privateErr := make(chan error, 1)
	go func() { privateErr <- operation.Evaluate(ctx, output) }()
	newer, err := state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, 0644, "new")
	require.NoError(t, err)
	defer newer.File.OnRelease(ctx)
	require.NoError(t, <-privateErr)
	defer output.OnRelease(ctx)
	body, _ := producedFileContents(t, output)
	require.Equal(t, "old", string(body))
	require.Equal(t, digest.FromString("new"), state.ContentDigest)
	require.Equal(t, "new", state.ETag)
	require.EqualValues(t, 2, plain.Load())
	require.EqualValues(t, 1, conditional.Load())

	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*HTTPState]{}))
	unavailable := &HTTPState{URL: origin.URL}
	stateResult := attachTransferObject(t, ctx, cache, srv, "operation-execution", "unavailableHTTPState", unavailable)
	stateID, err := cache.PersistedResultID(stateResult)
	require.NoError(t, err)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "_resolve", Receiver: &dagql.ResultCallRef{ResultID: stateID}, Type: dagql.NewResultCallType(output.Type())}
	guard := &operationStateReadGuard{SnapshotManager: store.Manager}
	server.cacheManager = guard
	unavailable.snapshotID = "unavailable-state-snapshot"
	unavailable.mu.Lock()
	defer unavailable.mu.Unlock()
	privateCtx, cancel := context.WithTimeout(dagql.ContextWithCall(ctx, frame), 5*time.Second)
	defer cancel()
	independent := decodedHTTPLazyOperation(t, privateCtx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "independent", Permissions: 0644, BodyDigest: old.ContentDigest})
	independentOutput := freshLazyOperationFile()
	done := make(chan error, 1)
	go func() { done <- independent.Evaluate(privateCtx, independentOutput) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-privateCtx.Done():
		t.Fatal("private HTTP operation accessed the unavailable state's lock")
	}
	require.NoError(t, independentOutput.OnRelease(ctx))
	require.Zero(t, guard.reads.Load(), "private HTTP operation tried to open retained state")
	require.Equal(t, "unavailable-state-snapshot", unavailable.snapshotID)
	require.Empty(t, unavailable.ETag)
}

type operationStateReadGuard struct {
	bkcache.SnapshotManager
	reads atomic.Int64
}

func (m *operationStateReadGuard) GetBySnapshotID(context.Context, string, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	m.reads.Add(1)
	return nil, errors.New("retained HTTP state is unavailable")
}

func TestEvaluatedLazyOperationConcurrentDemand(t *testing.T) {
	ctx := executionContext(t)
	requests := atomic.Int64{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, "body") }))
	defer origin.Close()
	saved := &FileHTTPResolveLazy{URL: origin.URL, Filename: "data", BodyDigest: digest.FromString("body")}
	operation := decodedHTTPLazyOperation(t, ctx, saved)
	output := freshLazyOperationFile()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- operation.Evaluate(ctx, output) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, requests.Load())
	require.NoError(t, output.OnRelease(ctx))
	a, b := decodedHTTPLazyOperation(t, ctx, saved), decodedHTTPLazyOperation(t, ctx, saved)
	require.NotSame(t, a.LazyMu, b.LazyMu)
	for _, lazy := range []*FileHTTPResolveLazy{a, b} {
		file := freshLazyOperationFile()
		require.NoError(t, lazy.Evaluate(ctx, file))
		require.NoError(t, file.OnRelease(ctx))
	}
	require.EqualValues(t, 3, requests.Load())
}
