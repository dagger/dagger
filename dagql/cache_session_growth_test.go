package dagql

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// cacheGrowthHeap is the live heap after a full collection.
func cacheGrowthHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func cacheGrowthCall(i int) *ResultCall {
	return &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(NewString("").Type()),
		Field: "growth",
		Args: []*ResultCallArg{{
			Name: "n",
			Value: &ResultCallLiteral{
				Kind:        ResultCallLiteralKindString,
				StringValue: fmt.Sprintf("call-%d", i),
			},
		}},
	}
}

// TestCacheSessionRetainsEveryResult reproduces the engine's unbounded
// in-session memory: every result a session touches stays owned by the
// session (sessionResultIDsBySession) until ReleaseSession, even when no
// caller holds it any more. Heap grows linearly with the number of distinct
// calls a session makes, and only session release gives it back.
//
// It measures, rather than asserts, the bytes per result, logging them;
// what it asserts is the retention: every result stays until release, and
// release frees them.
func TestCacheSessionRetainsEveryResult(t *testing.T) {
	const (
		calls       = 20000
		checkpoints = 4
		payload     = 1024
	)
	ctx := t.Context()
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const session = "long-session"
	value := strings.Repeat("x", payload)
	base := cacheGrowthHeap()
	var prev uint64 = base
	for i := 0; i < calls; i++ {
		frame := cacheGrowthCall(i)
		// The caller drops the result at once, as a client does once it
		// has the response: only the session's ownership keeps it.
		_, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			// A fresh copy per result, so each value is its own allocation.
			return NewResultForCall(NewString(string([]byte(value))), frame)
		})
		assert.NilError(t, err)
		if (i+1)%(calls/checkpoints) == 0 {
			heap := cacheGrowthHeap()
			c.egraphMu.RLock()
			results := len(c.resultsByID)
			c.egraphMu.RUnlock()
			c.sessionMu.Lock()
			held := len(c.sessionResultIDsBySession[session])
			c.sessionMu.Unlock()
			assert.Equal(t, results, i+1, "every result stays in the cache while its session lives")
			assert.Equal(t, held, i+1, "the session owns every result it touched")
			t.Logf("after %6d calls: %6d results, session holds %6d; heap +%6.1f MB (%4d B/result total, %4d B/result this step, payload %d B)",
				i+1, results, held, float64(heap-base)/1e6,
				int64(heap-base)/int64(i+1), int64(heap-prev)/int64(calls/checkpoints), payload)
			prev = heap
		}
	}
	grown := cacheGrowthHeap() - base
	// Linear growth: at least the payloads themselves stay resident.
	assert.Assert(t, grown > uint64(calls*payload), "heap grew %d B for %d results of %d B", grown, calls, payload)

	// The engine's memory-pressure GC (engine/server/gc.go) prunes by this
	// structural estimate, and only cold persisted roots: a live session's
	// results are active roots, so even an estimate over the maximum frees
	// nothing.
	estimate := c.MetadataEstimate()
	t.Logf("metadata estimate: %.1f MB for %d results (%d B/result; measured %d B/result)",
		float64(estimate.EstimatedBytes)/1e6, estimate.ResultCount,
		estimate.EstimatedBytes/int64(estimate.ResultCount), int64(grown)/calls)
	report, err := c.PruneMetadataEstimate(ctx, estimate.EstimatedBytes-1, 1)
	assert.NilError(t, err)
	assert.Assert(t, report.Triggered)
	c.egraphMu.RLock()
	results := len(c.resultsByID)
	c.egraphMu.RUnlock()
	t.Logf("metadata prune over the maximum: %d candidates, %d roots removed, %d results left",
		report.CandidateCount, report.RemovedPersistedRootCount, results)
	assert.Equal(t, results, calls, "memory-pressure prune frees nothing a live session holds")

	assert.NilError(t, c.ReleaseSession(ctx, session))
	assert.NilError(t, c.WaitSessionRelease(ctx, session))
	after := cacheGrowthHeap()
	c.egraphMu.RLock()
	results = len(c.resultsByID)
	c.egraphMu.RUnlock()
	t.Logf("after ReleaseSession: %d results; heap +%.1f MB over baseline (was +%.1f MB)",
		results, (float64(after)-float64(base))/1e6, float64(grown)/1e6)
	assert.Equal(t, results, 0, "session release frees every result it alone owned")
	assert.Assert(t, after < base+grown/4, "release returns the heap: before %d, after %d (baseline %d)", base+grown, after, base)
}
