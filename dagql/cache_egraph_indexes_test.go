package dagql

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"
)

func assertCacheDerivedIndexesConsistent(t testing.TB, c *Cache) {
	t.Helper()
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	assertCacheDerivedIndexesConsistentLocked(t, c)
}

func assertCacheDerivedIndexesConsistentLocked(t testing.TB, c *Cache) {
	t.Helper()
	assert.Assert(t, len(c.egraphTerms) == 0 || len(c.resultOutputEqClasses) > 0,
		"e-graph has %d terms but no result/output eq-class associations", len(c.egraphTerms))

	for resID, outputEqIDs := range c.resultOutputEqClasses {
		seenRoots := make(map[eqClassID]struct{}, len(outputEqIDs))
		for outputEqID := range outputEqIDs {
			root := c.findEqClassLocked(outputEqID)
			assert.Equal(t, outputEqID, root,
				"result %d has non-root output eq class %d (root %d)", resID, outputEqID, root)
			_, duplicateRoot := seenRoots[root]
			assert.Assert(t, !duplicateRoot,
				"result %d has duplicate associations for output eq-class root %d", resID, root)
			seenRoots[root] = struct{}{}
			results := c.outputEqClassResults[root]
			_, found := results[resID]
			assert.Assert(t, found,
				"result %d output eq class %d is missing inverse membership", resID, root)
		}
	}

	for outputEqID, results := range c.outputEqClassResults {
		root := c.findEqClassLocked(outputEqID)
		assert.Equal(t, outputEqID, root,
			"inverse output eq-class key %d is not a root (root %d)", outputEqID, root)
		for resID := range results {
			assert.Assert(t, c.resultsByID[resID] != nil,
				"inverse output eq class %d references missing result %d", outputEqID, resID)
			_, found := c.resultOutputEqClasses[resID][root]
			assert.Assert(t, found,
				"inverse output eq class %d result %d is missing forward membership", root, resID)
		}
	}
	nowUnix := time.Now().Unix()
	liveOutputRoots := make(map[eqClassID]struct{}, len(c.outputEqClassToTerms)+len(c.outputEqClassResults))
	for outputEqID := range c.outputEqClassToTerms {
		root := c.findEqClassLocked(outputEqID)
		if root != 0 {
			liveOutputRoots[root] = struct{}{}
		}
	}
	for outputEqID := range c.outputEqClassResults {
		root := c.findEqClassLocked(outputEqID)
		if root != 0 {
			liveOutputRoots[root] = struct{}{}
		}
	}
	for root := range liveOutputRoots {
		oldSemanticsHasUnexpiredResult := false
		for dig := range c.eqClassToDigests[root] {
			posting := c.egraphResultsByDigest[dig]
			if posting == nil {
				continue
			}
			for resID := range posting.Items() {
				res := c.resultsByID[resID]
				if res != nil && !c.resultExpiredAtLocked(res, nowUnix) {
					oldSemanticsHasUnexpiredResult = true
					break
				}
			}
			if oldSemanticsHasUnexpiredResult {
				break
			}
		}
		assert.Equal(t,
			oldSemanticsHasUnexpiredResult,
			c.hasUnexpiredResultForOutputEqClassLocked(root, nowUnix),
			"output eq-class root %d survivor predicate differs from digest-posting semantics", root,
		)
	}
}

func cacheTestSessionContext(ctx context.Context, sessionID string) context.Context {
	return engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  sessionID + "-client",
		SessionID: sessionID,
	})
}

func cacheTestPublishContentResult(
	t *testing.T,
	c *Cache,
	ctx context.Context,
	sessionID string,
	value int,
	contentDigest digest.Digest,
) AnyResult {
	t.Helper()
	requestCall := cacheTestIntCall(sessionID + "-request")
	responseCall := cacheTestIntCall(sessionID + "-response")
	res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(ctx context.Context) (AnyResult, error) {
		return cacheTestIntResult(responseCall, value).(Result[Int]).WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())
	return res
}

func TestCacheOutputEqClassInverseMixedSurvivor(t *testing.T) {
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	contentDigest := digest.FromString("inverse-mixed-survivor")
	ctxA := cacheTestSessionContext(baseCtx, "inverse-mixed-a")
	ctxB := cacheTestSessionContext(baseCtx, "inverse-mixed-b")
	resA := cacheTestPublishContentResult(t, c, ctxA, "inverse-mixed-a", 1, contentDigest)
	resB := cacheTestPublishContentResult(t, c, ctxB, "inverse-mixed-b", 2, contentDigest)

	sharedA := resA.cacheSharedResult()
	sharedB := resB.cacheSharedResult()
	assertCacheDerivedIndexesConsistent(t, c)

	c.egraphMu.Lock()
	classes := c.outputEqClassesForResultLocked(sharedA.id)
	assert.Equal(t, 1, len(classes))
	var outputEqID eqClassID
	for outputEqID = range classes {
	}
	termCount := len(c.outputEqClassToTerms[outputEqID])
	c.egraphMu.Unlock()
	assert.Assert(t, termCount > 0)

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-mixed-a"))

	c.egraphMu.Lock()
	_, removedPresent := c.outputEqClassResults[outputEqID][sharedA.id]
	_, survivorPresent := c.outputEqClassResults[outputEqID][sharedB.id]
	assert.Assert(t, !removedPresent)
	assert.Assert(t, survivorPresent)
	assert.Equal(t, termCount, len(c.outputEqClassToTerms[outputEqID]))
	assertCacheDerivedIndexesConsistentLocked(t, c)
	c.egraphMu.Unlock()

	assert.NilError(t, c.ReleaseSession(ctxB, "inverse-mixed-b"))
	assert.Equal(t, 0, c.Size())
}

func TestCacheOutputEqClassInverseExpiredOnlyDoesNotSurvive(t *testing.T) {
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	contentDigest := digest.FromString("inverse-expired-only")
	ctxA := cacheTestSessionContext(baseCtx, "inverse-expired-a")
	ctxB := cacheTestSessionContext(baseCtx, "inverse-expired-b")
	ctxKeeper := cacheTestSessionContext(baseCtx, "inverse-expired-keeper")
	resA := cacheTestPublishContentResult(t, c, ctxA, "inverse-expired-a", 1, contentDigest)
	resB := cacheTestPublishContentResult(t, c, ctxB, "inverse-expired-b", 2, contentDigest)
	keeperCall := cacheTestIntCall("inverse-expired-keeper")
	_, err = c.GetOrInitCall(ctxKeeper, "inverse-expired-keeper", noopTypeResolver{}, &CallRequest{ResultCall: keeperCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keeperCall, 3), nil
	})
	assert.NilError(t, err)

	sharedA := resA.cacheSharedResult()
	sharedB := resB.cacheSharedResult()
	c.egraphMu.Lock()
	classes := c.outputEqClassesForResultLocked(sharedA.id)
	assert.Equal(t, 1, len(classes))
	var outputEqID eqClassID
	for outputEqID = range classes {
	}
	removedTermIDs := make([]egraphTermID, 0, len(c.outputEqClassToTerms[outputEqID]))
	for termID := range c.outputEqClassToTerms[outputEqID] {
		removedTermIDs = append(removedTermIDs, termID)
	}
	sharedB.expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()
	assert.Assert(t, len(removedTermIDs) > 0)

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-expired-a"))

	c.egraphMu.Lock()
	assert.Assert(t, c.resultsByID[sharedB.id] == sharedB)
	for _, termID := range removedTermIDs {
		assert.Assert(t, c.egraphTerms[termID] == nil, "expired-only output term %d was retained", termID)
	}
	assertCacheDerivedIndexesConsistentLocked(t, c)
	c.egraphMu.Unlock()

	assert.NilError(t, c.ReleaseSession(ctxB, "inverse-expired-b"))
	assert.NilError(t, c.ReleaseSession(ctxKeeper, "inverse-expired-keeper"))
	assert.Equal(t, 0, c.Size())
}

func TestCacheOutputEqClassInverseMergeThenRemoval(t *testing.T) {
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctxA := cacheTestSessionContext(baseCtx, "inverse-merge-a")
	ctxB := cacheTestSessionContext(baseCtx, "inverse-merge-b")
	resA := cacheTestPublishContentResult(t, c, ctxA, "inverse-merge-a", 1, digest.FromString("inverse-merge-a"))
	resB := cacheTestPublishContentResult(t, c, ctxB, "inverse-merge-b", 2, digest.FromString("inverse-merge-b"))
	sharedA := resA.cacheSharedResult()
	sharedB := resB.cacheSharedResult()

	c.egraphMu.Lock()
	classesA := c.outputEqClassesForResultLocked(sharedA.id)
	classesB := c.outputEqClassesForResultLocked(sharedB.id)
	assert.Equal(t, 1, len(classesA))
	assert.Equal(t, 1, len(classesB))
	var rootA, rootB eqClassID
	for rootA = range classesA {
	}
	for rootB = range classesB {
	}
	assert.Assert(t, rootA != rootB)
	// Exercise the idempotent result-already-in-both-roots merge case.
	c.addResultOutputEqClassLocked(sharedA.id, rootB)
	mergedRoot := c.mergeEqClassesLocked(baseCtx, rootA, rootB)
	assert.Assert(t, mergedRoot != 0)
	assert.DeepEqual(t, c.resultOutputEqClasses[sharedA.id], map[eqClassID]struct{}{mergedRoot: {}})
	assert.DeepEqual(t, c.resultOutputEqClasses[sharedB.id], map[eqClassID]struct{}{mergedRoot: {}})
	assert.Equal(t, 2, len(c.outputEqClassResults[mergedRoot]))
	assertCacheDerivedIndexesConsistentLocked(t, c)
	c.egraphMu.Unlock()

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-merge-a"))
	assertCacheDerivedIndexesConsistent(t, c)
	assert.NilError(t, c.ReleaseSession(ctxB, "inverse-merge-b"))
	assert.Equal(t, 0, c.Size())
}

func TestCacheOutputEqClassInverseForcedCompactionAndReset(t *testing.T) {
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctxA := cacheTestSessionContext(baseCtx, "inverse-compact-a")
	ctxB := cacheTestSessionContext(baseCtx, "inverse-compact-b")
	cacheTestPublishContentResult(t, c, ctxA, "inverse-compact-a", 1, digest.FromString("inverse-compact-a"))
	cacheTestPublishContentResult(t, c, ctxB, "inverse-compact-b", 2, digest.FromString("inverse-compact-b"))

	c.egraphMu.Lock()
	for i := range 8 {
		c.ensureEqClassForDigestLocked(baseCtx, fmt.Sprintf("inverse-compact-dead-%d", i))
	}
	changed, oldSlots, newSlots := c.compactEqClassesLocked(true)
	assert.Assert(t, changed)
	assert.Assert(t, oldSlots > newSlots)
	assertCacheDerivedIndexesConsistentLocked(t, c)
	c.egraphMu.Unlock()

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-compact-a"))
	assertCacheDerivedIndexesConsistent(t, c)
	assert.NilError(t, c.ReleaseSession(ctxB, "inverse-compact-b"))

	c.egraphMu.Lock()
	assert.Assert(t, c.outputEqClassResults == nil)
	assert.Assert(t, c.resultOutputEqClasses == nil)
	c.egraphMu.Unlock()
}

func TestCacheReleaseCascadePreservesCallbacksAndCleansDerivedIndexes(t *testing.T) {
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctxParent := cacheTestSessionContext(baseCtx, "inverse-cascade-parent")
	ctxChild := cacheTestSessionContext(baseCtx, "inverse-cascade-child")
	var callbacks []string

	parentCall := cacheTestIntCall("inverse-cascade-parent")
	parent, err := c.GetOrInitCall(ctxParent, "inverse-cascade-parent", noopTypeResolver{}, &CallRequest{ResultCall: parentCall}, ValueFunc(
		cacheTestIntResultWithOnRelease(parentCall, 1, func(context.Context) error {
			callbacks = append(callbacks, "parent")
			return nil
		}),
	))
	assert.NilError(t, err)
	childCall := cacheTestIntCall("inverse-cascade-child")
	child, err := c.GetOrInitCall(ctxChild, "inverse-cascade-child", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, ValueFunc(
		cacheTestIntResultWithOnRelease(childCall, 2, func(context.Context) error {
			callbacks = append(callbacks, "child")
			return nil
		}),
	))
	assert.NilError(t, err)
	assert.NilError(t, c.AddExplicitDependency(ctxParent, parent, child, "derived-index-cascade"))

	c.egraphMu.Lock()
	assert.Equal(t, int64(2), child.cacheSharedResult().incomingOwnershipCount)
	assertCacheDerivedIndexesConsistentLocked(t, c)
	c.egraphMu.Unlock()
	assert.NilError(t, c.ReleaseSession(ctxChild, "inverse-cascade-child"))
	assert.NilError(t, c.ReleaseSession(ctxParent, "inverse-cascade-parent"))

	assert.Assert(t, slices.Equal(callbacks, []string{"parent", "child"}), "callback order: %v", callbacks)
	assert.Equal(t, 0, c.Size())
	c.egraphMu.Lock()
	assert.Assert(t, c.outputEqClassResults == nil)
	c.egraphMu.Unlock()
}
