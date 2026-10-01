package dagql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func cacheTestStringCall(field string) *ResultCall {
	return &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(String("").Type()),
		Field: field,
	}
}

func cacheTestPublishString(t *testing.T, ctx context.Context, c *Cache, field string, size int, persistable bool) AnyResult {
	t.Helper()
	frame := cacheTestStringCall(field)
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    frame,
		IsPersistable: persistable,
	}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(NewString(strings.Repeat("x", size)), frame)
	})
	assert.NilError(t, err)
	return res
}

func TestCacheMetadataEstimateCountsPayloadBytes(t *testing.T) {
	t.Parallel()

	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	c.putResultLocked(&sharedResult{id: 1, payloadBytes: 1000})
	c.putResultLocked(&sharedResult{id: 2, payloadBytes: 24})

	estimate := c.cacheMetadataEstimateLocked()
	assert.Equal(t, int64(1024), estimate.PayloadBytes)
	assert.Equal(t, 2*cacheMetadataResultEstimatedBytes+1024, estimate.EstimatedBytes)
}

func TestCacheResultPayloadBytesTrackRegistration(t *testing.T) {
	t.Parallel()

	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	a := &sharedResult{id: 1, payloadBytes: 100}
	c.putResultLocked(a)
	c.putResultLocked(a)
	assert.Equal(t, int64(100), c.resultPayloadBytes)

	// Replacing the registered result under its ID swaps its contribution.
	b := &sharedResult{id: 1, payloadBytes: 7}
	c.putResultLocked(b)
	assert.Equal(t, int64(7), c.resultPayloadBytes)

	// Neither a stale pointer nor an unregistered result moves the total.
	c.deleteResultLocked(a)
	c.setResultPayloadBytesLocked(a, 50)
	assert.Equal(t, int64(7), c.resultPayloadBytes)
	assert.Equal(t, int64(50), a.payloadBytes)

	c.setResultPayloadBytesLocked(b, 30)
	assert.Equal(t, int64(30), c.resultPayloadBytes)
	c.deleteResultLocked(b)
	assert.Equal(t, int64(0), c.resultPayloadBytes)
	assert.Equal(t, 0, len(c.resultsByID))
}

func TestCachePayloadBytesFollowPublishAndRelease(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const size = 256 << 10
	cacheTestPublishString(t, ctx, c, "payload-publish", size, false)
	estimate := c.MetadataEstimate()
	assert.Equal(t, int64(size), estimate.PayloadBytes)
	assert.Equal(t, 1, estimate.ResultCount)
	assert.Assert(t, estimate.EstimatedBytes > int64(size))

	cacheTestReleaseSession(t, c, ctx)
	assert.DeepEqual(t, c.MetadataEstimate(), CacheMetadataEstimate{})
}

func TestCachePruneMetadataEstimateCreditsPayloadBytes(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const size = 1 << 20
	large := cacheTestPublishString(t, ctx, c, "payload-prune-large", size, true)
	small := cacheTestPublishString(t, ctx, c, "payload-prune-small", 1, true)
	cacheTestReleaseSession(t, c, ctx)

	// Make the large result the coldest candidate.
	c.egraphMu.Lock()
	large.cacheSharedResult().lastUsedAtUnixNano = 1
	large.cacheSharedResult().createdAtUnixNano = 1
	c.egraphMu.Unlock()

	before := c.MetadataEstimate()
	assert.Equal(t, int64(size+1), before.PayloadBytes)
	directBytes := metadataDirectResultBytes(before)
	snapshot := c.snapshotPruneState(pruneSnapshotMetadata, directBytes)
	assert.Equal(t, directBytes+size, snapshot.results[large.cacheSharedResult().id].directResultBytes)
	assert.Equal(t, directBytes+1, snapshot.results[small.cacheSharedResult().id].directResultBytes)

	// Removing the large root alone reaches a target below the structural
	// share of both results.
	report, err := c.PruneMetadataEstimate(ctx, before.EstimatedBytes-1, before.EstimatedBytes-size)
	assert.NilError(t, err)
	assert.Assert(t, report.Triggered)
	assert.Equal(t, 1, report.PlannedRootCount)
	assert.Equal(t, directBytes+size, report.SimulatedStructuralBytes)
	assert.Equal(t, int64(1), report.AfterPrune.PayloadBytes)
	assert.Equal(t, 1, report.AfterPrune.ResultCount)
}

func TestCacheUpdateDecodedPayloadBytesRespectsRevision(t *testing.T) {
	t.Parallel()

	env := &PersistedResultEnvelope{ScalarJSON: json.RawMessage(`"` + strings.Repeat("y", 98) + `"`)}
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	res := &sharedResult{id: 1, payloadBytes: persistedEnvelopePayloadBytes(env), payloadRevision: 3}
	c.putResultLocked(res)
	assert.Equal(t, int64(100), c.resultPayloadBytes)

	// A later payload change wins over a stale decode measurement.
	c.updateDecodedPayloadBytes(res, NewString("stale"), 2)
	assert.Equal(t, int64(100), c.resultPayloadBytes)

	c.updateDecodedPayloadBytes(res, NewString(strings.Repeat("y", 98)), 3)
	assert.Equal(t, int64(98), c.resultPayloadBytes)
}

func TestPersistedEnvelopePayloadBytesCountsItems(t *testing.T) {
	t.Parallel()

	env := &PersistedResultEnvelope{
		ObjectJSON: json.RawMessage(`{"a":1}`),
		Items: []PersistedResultEnvelope{
			{ScalarJSON: json.RawMessage(`"abc"`)},
			{Items: []PersistedResultEnvelope{{ObjectJSON: json.RawMessage(`{}`)}}},
		},
	}
	assert.Equal(t, int64(7+5+2), persistedEnvelopePayloadBytes(env))
	assert.Equal(t, int64(0), persistedEnvelopePayloadBytes(nil))
}

func TestCachePayloadBytesUnwrapsNullable(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(3), cachePayloadBytes(NonNull(NewString("abc"))))
	assert.Equal(t, int64(0), cachePayloadBytes(Null[String]()))
	assert.Equal(t, int64(0), cachePayloadBytes(NewInt(1)))
	assert.Equal(t, int64(0), cachePayloadBytes(nil))
}
