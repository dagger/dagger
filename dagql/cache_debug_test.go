package dagql

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

type debugSnapshotQueuedWriter struct {
	bytes.Buffer
	cache      *Cache
	once       sync.Once
	writerDone chan struct{}
}

func (w *debugSnapshotQueuedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		go func() {
			w.cache.egraphMu.Lock()
			close(w.writerDone)
			w.cache.egraphMu.Unlock()
		}()
		// The snapshot owns a read lock at this flush. Wait until the writer
		// queues, so a recursive read would block deterministically.
		deadline := time.Now().Add(time.Second)
		for w.cache.egraphMu.TryRLock() {
			w.cache.egraphMu.RUnlock()
			if time.Now().After(deadline) {
				panic("debug snapshot writer did not queue")
			}
			runtime.Gosched()
		}
	})
	return w.Buffer.Write(p)
}

func TestDebugCacheSnapshotWithQueuedWriter(t *testing.T) {
	for _, queueWriter := range []bool{false, true} {
		name := "without-writer"
		if queueWriter {
			name = "with-writer"
		}
		t.Run(name, func(t *testing.T) {
			content := call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: "sha256:first-content"}
			first := cacheTestIntCall("first", content)
			second := cacheTestIntCall("second")
			second.Receiver = &ResultCallRef{ResultID: 1}
			c := &Cache{resultsByID: map[sharedResultID]*sharedResult{
				1: {id: 1, resultCall: first, description: strings.Repeat("x", 256<<10)},
				2: {id: 2, resultCall: second},
			}}
			var out bytes.Buffer
			var w io.Writer = &out
			var queued *debugSnapshotQueuedWriter
			if queueWriter {
				queued = &debugSnapshotQueuedWriter{cache: c, writerDone: make(chan struct{})}
				w = queued
			}
			done := make(chan error, 1)
			go func() { done <- c.WriteDebugCacheSnapshot(w) }()
			select {
			case err := <-done:
				assert.NilError(t, err)
			case <-time.After(5 * time.Second):
				stack := make([]byte, 64<<10)
				n := runtime.Stack(stack, true)
				t.Fatalf("debug snapshot did not finish:\n%s", stack[:n])
			}
			if queued != nil {
				select {
				case <-queued.writerDone:
				case <-time.After(time.Second):
					t.Fatal("writer did not finish after debug snapshot")
				}
				out = queued.Buffer
			}
			var snapshot CacheDebugSnapshot
			assert.NilError(t, json.Unmarshal(out.Bytes(), &snapshot))
			assert.Assert(t, is.Len(snapshot.Results, 2))

			// Inline references compute the same expected digests without warming
			// the cache-owned frames used by the snapshot above.
			expectedFirst := cacheTestIntCall("first", content)
			expectedSecond := cacheTestIntCall("second")
			expectedSecond.Receiver = &ResultCallRef{Call: expectedFirst}
			for i, frame := range []*ResultCall{expectedFirst, expectedSecond} {
				recipe, err := frame.deriveRecipeDigest(nil)
				assert.NilError(t, err)
				preferred, err := frame.deriveContentPreferredDigest(nil)
				assert.NilError(t, err)
				result := snapshot.Results[i]
				assert.Assert(t, result.ResultCallRecipeDigest != "")
				assert.Equal(t, result.ResultCallRecipeDigest, recipe.String())
				assert.Equal(t, result.ResultCallContentPreferredDigest, preferred.String())
				assert.Equal(t, result.ResultCallRecipeDigestError, "")
				assert.Equal(t, result.ResultCallContentPreferredDigestError, "")
				assert.Equal(t, result.ResultCallInputDigestsError, "")
			}
			assert.DeepEqual(t, snapshot.Results[1].ResultCallInputDigests, []string{snapshot.Results[0].ResultCallRecipeDigest})
		})
	}
}

func TestDebugCacheSnapshotIncludesResultMetadata(t *testing.T) {
	base, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	c := base

	attached, err := c.AttachResult(t.Context(), "test-session", noopTypeResolver{}, cacheTestDetachedResult(cacheTestIntCall("debugCache"), NewInt(123)))
	assert.NilError(t, err)
	shared := attached.cacheSharedResult()
	assert.Assert(t, shared != nil)

	var out bytes.Buffer
	err = c.WriteDebugCacheSnapshot(&out)
	assert.NilError(t, err)

	var snapshot CacheDebugSnapshot
	err = json.Unmarshal(out.Bytes(), &snapshot)
	assert.NilError(t, err)
	assert.Assert(t, is.Len(snapshot.Results, 1))
	assert.Assert(t, is.Len(snapshot.ResultDigestIndexes, 1))

	result := snapshot.Results[0]
	assert.Equal(t, result.SharedResultID, uint64(shared.id))
	assert.Assert(t, result.ResultCall != nil)
	assert.Equal(t, result.ResultCall.Field, "debugCache")
	assert.Assert(t, result.ResultCallRecipeDigest != "")
	assert.Assert(t, result.ResultCallRecipeDigestError == "")
	assert.Assert(t, slices.Contains(result.IndexedDigests, result.ResultCallRecipeDigest))
	assert.Assert(t, len(result.AssociatedTermIDs) > 0)

	index := snapshot.ResultDigestIndexes[0]
	assert.Equal(t, index.Digest, result.ResultCallRecipeDigest)
	assert.DeepEqual(t, index.SharedResultIDs, []uint64{uint64(shared.id)})
}

func TestDebugCacheSnapshotIncludesCompletedArbitraryCalls(t *testing.T) {
	base, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	c := base

	_, err = c.GetOrInitArbitrary(t.Context(), "debug-session", "debug-arbitrary", ArbitraryValueFunc("hello"))
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, c.ReleaseSession(t.Context(), "debug-session"))
	}()

	var out bytes.Buffer
	err = c.WriteDebugCacheSnapshot(&out)
	assert.NilError(t, err)

	var snapshot CacheDebugSnapshot
	err = json.Unmarshal(out.Bytes(), &snapshot)
	assert.NilError(t, err)
	assert.Assert(t, is.Len(snapshot.CompletedArbitraryCalls, 1))

	call := snapshot.CompletedArbitraryCalls[0]
	assert.Equal(t, call.CallKey, "debug-arbitrary")
	assert.Assert(t, call.Completed)
	assert.Assert(t, call.HasValue)
	assert.Equal(t, call.ValueType, "string")
	assert.Equal(t, call.OwnerSessionCount, 1)
}

// debugSnapshotEntry takes a debug snapshot of c and returns its entry for the
// result number, without loading the result.
func debugSnapshotEntry(t *testing.T, c *Cache, number uint64) CacheDebugResult {
	t.Helper()
	var out bytes.Buffer
	assert.NilError(t, c.WriteDebugCacheSnapshot(&out))
	var snapshot CacheDebugSnapshot
	assert.NilError(t, json.Unmarshal(out.Bytes(), &snapshot))
	for _, entry := range snapshot.Results {
		if entry.SharedResultID == number {
			return entry
		}
	}
	t.Fatalf("no snapshot entry for %d", number)
	return CacheDebugResult{}
}

// The snapshot reports an entry's complete parts from its current record, as
// a span does: an eager value's part, which no gate settled and no capture
// has read yet, is complete. While an evaluation is in flight the record
// can't be read, and the snapshot reports the settled subset as partial.
func TestDebugCacheSnapshotReportsCompleteParts(t *testing.T) {
	t.Parallel()
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)
	ctx, c, srv := statePartCache(t, filepath.Join(t.TempDir(), "cache.db"))
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })

	eagerValue := &statePartValue{}
	eagerValue.done.Store(true)
	eager := persistedListTestResult(t, ctx, c, srv, "eager", eagerValue)
	assert.Assert(t, eager.cacheSharedResult().completeParts.Load() == nil, "no capture has read the record yet")
	entry := debugSnapshotEntry(t, c, uint64(eager.cacheSharedResult().id))
	assert.DeepEqual(t, []string{snapshotKey}, entry.CompleteParts)
	assert.Assert(t, !entry.CompletePartsPartial)

	block := make(chan struct{})
	lazy := persistedListTestResult(t, ctx, c, srv, "lazy", &statePartValue{lazy: true, block: block})
	evaluated := make(chan error, 1)
	go func() { evaluated <- c.Evaluate(ctx, lazy) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, _, err := c.captureRowRecord(ctx, lazy.cacheSharedResult()); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the evaluation never made the record unreadable")
		}
		time.Sleep(time.Millisecond)
	}
	entry = debugSnapshotEntry(t, c, uint64(lazy.cacheSharedResult().id))
	assert.Equal(t, 0, len(entry.CompleteParts))
	assert.Assert(t, entry.CompletePartsPartial, "the record can't be read during the evaluation")
	close(block)
	assert.NilError(t, <-evaluated)
	entry = debugSnapshotEntry(t, c, uint64(lazy.cacheSharedResult().id))
	assert.DeepEqual(t, []string{snapshotKey}, entry.CompleteParts)
	assert.Assert(t, !entry.CompletePartsPartial)
}

// A merged entry the engine has not decoded reports its parts from its
// record: the parts the Cloud offers it, apart from its complete parts, and
// its Cloud holding; a record with a complete part reports that part
// complete.
func TestDebugCacheSnapshotReportsMergedParts(t *testing.T) {
	t.Parallel()
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)

	t.Run("offered", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestOffered(t, "root")
		ctx, c, _ := transferTestCache(t)
		reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
		assert.NilError(t, err)

		entry := debugSnapshotEntry(t, c, reply.Imported()[0].ResultID)
		assert.Equal(t, "imported_lazy_envelope", entry.PayloadState, "undecoded")
		assert.DeepEqual(t, []string{snapshotKey}, entry.OfferedParts)
		assert.Assert(t, !slices.Contains(entry.CompleteParts, snapshotKey), "an offered part is not complete")
		assert.Assert(t, !entry.CompletePartsPartial)
		assert.DeepEqual(t, &CacheDebugCloudHolding{Number: bundle.Values[0].SenderNumber, Stored: true, ExpiresAtUnix: bundle.Values[0].ExpiresAtUnix}, entry.CloudHolding)
	})

	t.Run("complete", func(t *testing.T) {
		t.Parallel()
		actx, a, asrv := statePartCache(t, filepath.Join(t.TempDir(), "a.db"))
		t.Cleanup(func() { assert.NilError(t, a.CloseDiscardingPersistence()) })
		value := &statePartValue{}
		value.done.Store(true)
		bundle := exportTestBundle(t, actx, a, persistedListTestResult(t, actx, a, asrv, "eager", value))
		ctx, c, _ := statePartCache(t, filepath.Join(t.TempDir(), "b.db"))
		t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
		reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
		assert.NilError(t, err)

		entry := debugSnapshotEntry(t, c, reply.Imported()[0].ResultID)
		assert.Equal(t, "imported_lazy_envelope", entry.PayloadState, "undecoded")
		assert.DeepEqual(t, []string{snapshotKey}, entry.CompleteParts)
		assert.Assert(t, !entry.CompletePartsPartial)
		assert.Equal(t, 0, len(entry.OfferedParts))
		assert.Assert(t, entry.CloudHolding != nil)
		assert.Equal(t, bundle.Values[0].SenderNumber, entry.CloudHolding.Number)
	})
}
