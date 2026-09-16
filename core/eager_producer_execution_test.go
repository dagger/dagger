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
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/moby/locker"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type producerExecutionServer struct {
	*cacheVolumeTestQueryServer
	srv     *dagql.Server
	store   content.Store
	builtin content.Store
	mountNS *os.File
	locker  *locker.Locker
}

func (s *producerExecutionServer) CleanMountNS() *os.File                        { return s.mountNS }
func (s *producerExecutionServer) Locker() *locker.Locker                        { return s.locker }
func (s *producerExecutionServer) DNS() *oci.DNSConfig                           { return &oci.DNSConfig{} }
func (s *producerExecutionServer) Server(context.Context) (*dagql.Server, error) { return s.srv, nil }
func (s *producerExecutionServer) OCIStore() content.Store                       { return s.store }
func (s *producerExecutionServer) BuiltinOCIStore() content.Store                { return s.builtin }
func (s *producerExecutionServer) Platform() Platform {
	return Platform{OS: "linux", Architecture: "amd64"}
}
func executionFixture(t *testing.T) (context.Context, *testutil.Store, *dagql.Cache, *dagql.Server, *producerExecutionServer) {
	t.Helper()
	store := testutil.NewStore(t)
	ctx, cache, srv := transferCache(t, store, "", "producer-execution")
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	server := &producerExecutionServer{cacheVolumeTestQueryServer: &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: store.Manager}, srv: srv, store: store.Content}
	type namespaceResult struct {
		file *os.File
		err  error
	}
	created := make(chan namespaceResult, 1)
	go func() {
		runtime.LockOSThread()
		// Exiting this goroutine retires the thread with its private mount namespace.
		if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
			created <- namespaceResult{err: err}
			return
		}
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			created <- namespaceResult{err: err}
			return
		}
		file, err := os.Open("/proc/thread-self/ns/mnt")
		created <- namespaceResult{file: file, err: err}
	}()
	namespace := <-created
	require.NoError(t, namespace.err)
	server.mountNS = namespace.file
	t.Cleanup(func() { require.NoError(t, server.mountNS.Close()) })
	server.locker = locker.New()
	query.Server = server
	return ctx, store, cache, srv, server
}
func freshProducerFile() *File {
	return &File{Platform: Platform{OS: "linux", Architecture: "arm64"}, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
}
func freshProducerDirectory() *Directory {
	return &Directory{Platform: Platform{OS: "linux", Architecture: "arm64"}, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
}
func decodedHTTPProducer(t *testing.T, ctx context.Context, lazy *FileHTTPResolveLazy) *FileHTTPResolveLazy {
	t.Helper()
	raw, err := lazy.EncodePersisted(ctx, nil)
	require.NoError(t, err)
	decoded, err := decodeFileHTTPResolveLazy(raw)
	require.NoError(t, err)
	return decoded.(*FileHTTPResolveLazy)
}
func producedFileContents(t *testing.T, ctx context.Context, file *File) ([]byte, os.FileInfo) {
	t.Helper()
	name, snapshot, err := producedFileOutput(file)
	require.NoError(t, err)
	var data []byte
	var info os.FileInfo
	require.NoError(t, MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
		path, err := RootPathWithoutFinalSymlink(root, name)
		if err != nil {
			return err
		}
		data, err = os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err = os.Stat(path)
		return err
	}))
	return data, info
}
func TestHTTPCompletedProducerEvaluate(t *testing.T) {
	ctx, _, _, _, _ := executionFixture(t)
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
			lazy := decodedHTTPProducer(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "data", Permissions: 0644, Checksum: dagql.Optional[dagql.String]{Valid: test.valid, Value: dagql.String(test.checksum)}, BodyDigest: digest.FromString("saved")})
			file := freshProducerFile()
			err := lazy.Evaluate(ctx, file)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				_, ready := file.Snapshot.Peek()
				require.False(t, ready)
			} else {
				require.NoError(t, err)
				data, _ := producedFileContents(t, ctx, file)
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
		lazy := decodedHTTPProducer(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "empty", BodyDigest: digest.FromString("")})
		file := freshProducerFile()
		require.NoError(t, lazy.Evaluate(ctx, file))
		require.NoError(t, file.OnRelease(ctx))
		origin.Close()
	}
	t.Run("transport", func(t *testing.T) {
		lazy := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "http://127.0.0.1:1", Filename: "data", BodyDigest: digest.FromString("saved")}
		file := freshProducerFile()
		require.Error(t, lazy.Evaluate(ctx, file))
		_, ready := file.Snapshot.Peek()
		require.False(t, ready)
	})
	t.Run("canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		lazy := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "http://127.0.0.1:1", Filename: "data", BodyDigest: digest.FromString("saved")}
		require.ErrorIs(t, lazy.Evaluate(cctx, freshProducerFile()), context.Canceled)
	})
}

func TestHTTPProducerWriter(t *testing.T) {
	ctx, _, _, _, _ := executionFixture(t)
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
				output := freshProducerFile()
				producer := decodedHTTPProducer(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: name, Permissions: mode, BodyDigest: digest.FromString("saved")})
				restoreErr := producer.Evaluate(ctx, output)
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
				a, ai := producedFileContents(t, ctx, eager.File)
				b, bi := producedFileContents(t, ctx, output)
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
		old := syscall.Umask(0077)
		defer syscall.Umask(old)
		output := freshProducerFile()
		producer := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: origin.URL, Filename: "data", Permissions: 0755, BodyDigest: digest.FromString("saved")}
		require.NoError(t, producer.Evaluate(ctx, output))
		defer output.OnRelease(ctx)
		_, info := producedFileContents(t, ctx, output)
		require.EqualValues(t, 0755, info.Mode().Perm())
	})
	t.Run("public eager layout", func(t *testing.T) {
		old := syscall.Umask(0)
		defer syscall.Umask(old)
		for _, name := range []string{"data", "/data", "../data", "a/../data", "./data"} {
			output, err := FetchHTTPFile(ctx, query, FetchHTTPRequestOpts{URL: origin.URL, Filename: name, Permissions: 0644, AuthorizationHeader: "saved-auth"})
			require.NoError(t, err)
			require.Nil(t, output.File.Lazy)
			require.Nil(t, output.File.completedRecipe)
			require.Empty(t, output.File.completedRecipeJSON)
			path, snapshot, err := producedFileOutput(output.File)
			require.NoError(t, err)
			require.Equal(t, name, path)
			require.NoError(t, MountRef(ctx, snapshot, func(root string, _ *mount.Mount) error {
				data, err := os.ReadFile(filepath.Join(root, "data"))
				if name == "../data" {
					require.ErrorIs(t, err, os.ErrNotExist)
				} else {
					require.NoError(t, err)
					require.Equal(t, "saved", string(data))
				}
				return nil
			}))
			require.NoError(t, output.File.OnRelease(ctx))
		}
	})

}

type producerBodyTransport struct {
	http.RoundTripper
	closed *atomic.Int64
	onEOF  func()
}

func (t *producerBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err == nil {
		resp.Body = &producerObservedBody{ReadCloser: resp.Body, closed: t.closed, onEOF: t.onEOF}
	}
	return resp, err
}

type producerObservedBody struct {
	io.ReadCloser
	closed           *atomic.Int64
	onEOF            func()
	done, pendingEOF bool
}

func (b *producerObservedBody) Read(p []byte) (int, error) {
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
func (b *producerObservedBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

type producerObservedMount struct {
	bkcache.MountableRef
	root *string
}

func (m *producerObservedMount) Mount() ([]mount.Mount, func() error, error) {
	mounts, release, err := m.MountableRef.Mount()
	if err == nil && len(mounts) > 0 {
		*m.root = mounts[0].Source
	}
	return mounts, release, err
}

type observedProducerManager struct {
	bkcache.SnapshotManager
	mutableReleases, immutableReleases atomic.Int64
	commitErr                          error
	mountErr                           error
	writerRoot                         string
}

func (m *observedProducerManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	if p, ok := parent.(*observedProducerSnapshot); ok {
		parent = p.ImmutableRef
	}
	ref, err := m.SnapshotManager.New(ctx, parent, opts...)
	if err != nil {
		return nil, err
	}
	return &observedProducerMutable{MutableRef: ref, manager: m}, nil
}

type observedProducerMutable struct {
	bkcache.MutableRef
	manager *observedProducerManager
}

func (r *observedProducerMutable) Release(ctx context.Context) error {
	r.manager.mutableReleases.Add(1)
	return r.MutableRef.Release(ctx)
}
func (r *observedProducerMutable) Mount(ctx context.Context, ro bool) (bkcache.MountableRef, error) {
	if r.manager.mountErr != nil {
		return nil, r.manager.mountErr
	}
	ref, err := r.MutableRef.Mount(ctx, ro)
	if err != nil {
		return nil, err
	}
	return &producerObservedMount{MountableRef: ref, root: &r.manager.writerRoot}, nil
}
func (r *observedProducerMutable) Commit(ctx context.Context) (bkcache.ImmutableRef, error) {
	if r.manager.commitErr != nil {
		return nil, r.manager.commitErr
	}
	ref, err := r.MutableRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return &observedProducerSnapshot{ImmutableRef: ref, manager: r.manager}, nil
}

type observedProducerSnapshot struct {
	bkcache.ImmutableRef
	manager *observedProducerManager
}

func (r *observedProducerSnapshot) Release(ctx context.Context) error {
	r.manager.immutableReleases.Add(1)
	return r.ImmutableRef.Release(ctx)
}

func TestHTTPProducerCleanup(t *testing.T) {
	ctx, store, _, _, server := executionFixture(t)
	for _, exit := range []string{"copy", "close", "chmod", "timestamp", "checksum", "commit", "mount", "digest", "move", "success"} {
		t.Run(exit, func(t *testing.T) {
			if exit == "timestamp" && os.Getenv("DAGGER_TEST_HTTP_TIMESTAMP_FAULT") != "1" {
				tracer, err := exec.LookPath("strace")
				require.NoError(t, err)
				binary, err := os.Executable()
				require.NoError(t, err)
				cmd := exec.Command(tracer, "-f", "-qq", "-o", filepath.Join(t.TempDir(), "syscalls.log"), "-e", "trace=utimensat", "-e", "inject=utimensat:error=EIO:when=1", binary, "-test.run", "^TestHTTPProducerCleanup/timestamp$", "-test.count=1", "-test.v")
				cmd.Env = append(os.Environ(), "DAGGER_TEST_HTTP_TIMESTAMP_FAULT=1")
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))
				return
			}
			manager := &observedProducerManager{SnapshotManager: store.Manager}
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
			producer := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: origin.URL, Filename: "data", Permissions: 0644, BodyDigest: digest.FromString("body")}
			if exit == "digest" {
				producer.BodyDigest = digest.FromString("other")
			}
			if exit == "checksum" {
				producer.Checksum = dagql.Optional[dagql.String]{Valid: true, Value: dagql.String(digest.FromString("other"))}
			}
			output := freshProducerFile()
			closed := atomic.Int64{}
			previousTransport := http.DefaultTransport
			transport := &producerBodyTransport{RoundTripper: previousTransport, closed: &closed}
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
			err := producer.Evaluate(ctx, output)
			require.EqualValues(t, 1, closed.Load())
			if exit == "close" || exit == "chmod" {
				require.ErrorContains(t, err, exit)
			}
			if exit == "timestamp" {
				require.ErrorContains(t, err, "chtimes")
			}

			switch exit {
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
	ctx, _, _, _, _ := executionFixture(t)
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

func TestStatelessHTTPProducerIsolation(t *testing.T) {
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
	producer := decodedHTTPProducer(t, ctx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "old", Permissions: 0644, BodyDigest: old.ContentDigest})
	output := freshProducerFile()
	privateErr := make(chan error, 1)
	go func() { privateErr <- producer.Evaluate(ctx, output) }()
	newer, err := state.Resolve(ctx, query, dagql.Optional[dagql.String]{}, 0644, "new")
	require.NoError(t, err)
	defer newer.File.OnRelease(ctx)
	require.NoError(t, <-privateErr)
	defer output.OnRelease(ctx)
	body, _ := producedFileContents(t, ctx, output)
	require.Equal(t, "old", string(body))
	require.Equal(t, digest.FromString("new"), state.ContentDigest)
	require.Equal(t, "new", state.ETag)
	require.EqualValues(t, 2, plain.Load())
	require.EqualValues(t, 1, conditional.Load())

	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*HTTPState]{}))
	unavailable := &HTTPState{URL: origin.URL}
	stateResult := attachTransferObject(t, ctx, cache, srv, "producer-execution", "unavailableHTTPState", unavailable)
	stateID, err := cache.PersistedResultID(stateResult)
	require.NoError(t, err)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "_resolve", Receiver: &dagql.ResultCallRef{ResultID: stateID}, Type: dagql.NewResultCallType(output.Type())}
	guard := &producerStateReadGuard{SnapshotManager: store.Manager}
	server.cacheManager = guard
	unavailable.snapshotID = "unavailable-state-snapshot"
	unavailable.mu.Lock()
	defer unavailable.mu.Unlock()
	privateCtx, cancel := context.WithTimeout(dagql.ContextWithCall(ctx, frame), 5*time.Second)
	defer cancel()
	independent := decodedHTTPProducer(t, privateCtx, &FileHTTPResolveLazy{URL: origin.URL, Filename: "independent", Permissions: 0644, BodyDigest: old.ContentDigest})
	independentOutput := freshProducerFile()
	done := make(chan error, 1)
	go func() { done <- independent.Evaluate(privateCtx, independentOutput) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-privateCtx.Done():
		t.Fatal("private HTTP producer accessed the unavailable state's lock")
	}
	require.NoError(t, independentOutput.OnRelease(ctx))
	require.Zero(t, guard.reads.Load(), "private HTTP producer tried to open retained state")
	require.Equal(t, "unavailable-state-snapshot", unavailable.snapshotID)
	require.Empty(t, unavailable.ETag)
}

type producerStateReadGuard struct {
	bkcache.SnapshotManager
	reads atomic.Int64
}

func (m *producerStateReadGuard) GetBySnapshotID(context.Context, string, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	m.reads.Add(1)
	return nil, errors.New("retained HTTP state is unavailable")
}

func TestCompletedProducerConcurrentDemand(t *testing.T) {
	ctx, _, _, _, _ := executionFixture(t)
	requests := atomic.Int64{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, "body") }))
	defer origin.Close()
	saved := &FileHTTPResolveLazy{URL: origin.URL, Filename: "data", BodyDigest: digest.FromString("body")}
	producer := decodedHTTPProducer(t, ctx, saved)
	output := freshProducerFile()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- producer.Evaluate(ctx, output) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, requests.Load())
	require.NoError(t, output.OnRelease(ctx))
	a, b := decodedHTTPProducer(t, ctx, saved), decodedHTTPProducer(t, ctx, saved)
	require.NotSame(t, a.LazyMu, b.LazyMu)
	for _, lazy := range []*FileHTTPResolveLazy{a, b} {
		file := freshProducerFile()
		require.NoError(t, lazy.Evaluate(ctx, file))
		require.NoError(t, file.OnRelease(ctx))
	}
	require.EqualValues(t, 3, requests.Load())
}
