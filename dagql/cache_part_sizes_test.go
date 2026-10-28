package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// partSizeTestManager serves the usage each layer of a snapshot chain recorded
// and its parents, and the files that travel as their own content, counting
// the usage and file reads.
type partSizeTestManager struct {
	*fakeSnapshotManager
	mu     sync.Mutex
	usage  map[string]int64
	parent map[string]string
	// files maps "<snapshot>:<path>" to the size of a file that travels as
	// its own content.
	files map[string]int64
	reads int
}

func (m *partSizeTestManager) SnapshotUsage(_ context.Context, snapshotID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	usage, ok := m.usage[snapshotID]
	if !ok {
		return 0, fmt.Errorf("no usage for snapshot %q", snapshotID)
	}
	return usage, nil
}

func (m *partSizeTestManager) SnapshotParent(_ context.Context, snapshotID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.parent[snapshotID], nil
}

func (m *partSizeTestManager) OwnFileSize(_ context.Context, snapshotID, filePath string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	size, ok := m.files[snapshotID+":"+filePath]
	return size, ok, nil
}

func (m *partSizeTestManager) sizeReads() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reads
}

// partSizeTestCache is statePartCache over mgr, with the remote cache attached
// when remote is set.
func partSizeTestCache(t *testing.T, mgr *partSizeTestManager, remote bool) (context.Context, *Cache, *Server) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	if remote {
		_, created, err := c.AttachRemoteCacheBridge()
		assert.NilError(t, err)
		assert.Assert(t, created)
	}
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*statePartValue]{}))
	return srvToContext(ContextWithCache(ctx, c), srv), c, srv
}

// An engine with the remote cache reports each complete part's raw size beside
// it, on the lazy span that completes the part and on a later call's span. A
// directory's is the sum of the usage each layer of its snapshot chain
// recorded; a File that travels as its own content is its file's. An engine
// without the remote cache reports no size and reads none.
func TestCompletePartSizes(t *testing.T) {
	t.Parallel()
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)
	for _, tc := range []struct {
		name   string
		remote bool
	}{{"with the remote cache", true}, {"without the remote cache", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := &partSizeTestManager{
				fakeSnapshotManager: &fakeSnapshotManager{},
				usage:               map[string]int64{"top": 300, "mid": 20, "base": 1},
				parent:              map[string]string{"top": "mid", "mid": "base"},
				files:               map[string]int64{"files:/src/go.mod": 15},
			}
			ctx, c, srv := partSizeTestCache(t, mgr, tc.remote)
			rec := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
			t.Cleanup(func() { assert.NilError(t, tp.Shutdown(context.Background())) })

			values := map[string]AnyResult{
				"dir":  persistedListTestResult(t, ctx, c, srv, "dir", &statePartValue{lazy: true, snapshotID: "top"}),
				"file": persistedListTestResult(t, ctx, c, srv, "file", &statePartValue{lazy: true, snapshotID: "files", file: "/src/go.mod"}),
			}
			spanCtx, demand := tp.Tracer("test").Start(ctx, "demand")
			for _, value := range values {
				assert.NilError(t, c.Evaluate(spanCtx, value))
			}
			demand.End()

			want := map[string][]string{"dir": {"321"}, "file": {"15"}}
			for field, value := range values {
				id := strconv.FormatUint(uint64(value.cacheSharedResult().id), 10)
				var lazyParts, lazySizes []string
				var lazySpans int
				for _, span := range rec.Ended() {
					attrs := map[string][]string{}
					var named bool
					for _, kv := range span.Attributes() {
						switch kv.Key {
						case telemetryattrs.CacheResultIDAttr:
							named = kv.Value.AsString() == id
						case telemetryattrs.CachePartsAttr, telemetryattrs.CachePartSizesAttr:
							attrs[string(kv.Key)] = kv.Value.AsStringSlice()
						}
					}
					if named {
						lazySpans++
						lazyParts, lazySizes = attrs[telemetryattrs.CachePartsAttr], attrs[telemetryattrs.CachePartSizesAttr]
					}
				}
				assert.Equal(t, 1, lazySpans, "%s: one lazy span names the entry", field)
				assert.DeepEqual(t, []string{snapshotKey}, lazyParts)

				req := cacheTestArmedRequest(statePartFrame(field))
				req.IsPersistable = true
				hit, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
					t.Errorf("%s: a hit runs no resolver", field)
					return nil, nil
				})
				assert.NilError(t, err)
				state, ok := req.CacheEvidence.ResultState(ctx, hit)
				assert.Assert(t, ok)
				assert.DeepEqual(t, []string{snapshotKey}, state.Parts)

				var callSizes []string
				for _, size := range state.PartSizes {
					callSizes = append(callSizes, strconv.FormatInt(size, 10))
				}
				if tc.remote {
					assert.DeepEqual(t, want[field], lazySizes)
					assert.DeepEqual(t, want[field], callSizes)
				} else {
					assert.Assert(t, lazySizes == nil, "%s: no size without the remote cache", field)
					assert.Assert(t, state.PartSizes == nil, "%s: no size without the remote cache", field)
				}
			}
			if !tc.remote {
				assert.Equal(t, 0, mgr.sizeReads(), "an engine without the remote cache reads no size")
			}
		})
	}
}
