package dagql

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"
)

func assertCacheDerivedIndexesConsistent(t testing.TB, c *Cache) {
	t.Helper()
	c.egraphMu.Lock()
	err := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.NilError(t, err)
}

func cacheDerivedIndexesErrorLocked(c *Cache) error {
	if len(c.egraphTerms) != 0 && len(c.resultOutputEqClasses) == 0 {
		return fmt.Errorf("e-graph has %d terms but no result/output eq-class associations", len(c.egraphTerms))
	}

	for resID, outputEqIDs := range c.resultOutputEqClasses {
		seenRoots := make(map[eqClassID]struct{}, len(outputEqIDs))
		for outputEqID := range outputEqIDs {
			root := c.findEqClassLocked(outputEqID)
			if outputEqID != root {
				return fmt.Errorf("result %d has non-root output eq class %d (root %d)", resID, outputEqID, root)
			}
			_, duplicateRoot := seenRoots[root]
			if duplicateRoot {
				return fmt.Errorf("result %d has duplicate associations for output eq-class root %d", resID, root)
			}
			seenRoots[root] = struct{}{}
			results := c.outputEqClassResults[root]
			_, found := results[resID]
			if !found {
				return fmt.Errorf("result %d output eq class %d is missing inverse membership", resID, root)
			}
		}
	}

	for outputEqID, results := range c.outputEqClassResults {
		root := c.findEqClassLocked(outputEqID)
		if outputEqID != root {
			return fmt.Errorf("inverse output eq-class key %d is not a root (root %d)", outputEqID, root)
		}
		for resID := range results {
			if c.resultsByID[resID] == nil {
				return fmt.Errorf("inverse output eq class %d references missing result %d", outputEqID, resID)
			}
			_, found := c.resultOutputEqClasses[resID][root]
			if !found {
				return fmt.Errorf("inverse output eq class %d result %d is missing forward membership", root, resID)
			}
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
		if oldSemanticsHasUnexpiredResult != c.hasUnexpiredResultForOutputEqClassLocked(root, nowUnix) {
			return fmt.Errorf("output eq-class root %d survivor predicate differs from digest-posting semantics", root)
		}
	}

	exactDigestsByResult := make(map[sharedResultID]map[string]struct{}, len(c.resultIndexedDigests))
	for resID, digests := range c.resultIndexedDigests {
		if c.resultsByID[resID] == nil {
			return fmt.Errorf("exact digest index references missing result %d", resID)
		}
		seen := make(map[string]struct{}, len(digests))
		for _, dig := range digests {
			_, duplicate := seen[dig]
			if duplicate {
				return fmt.Errorf("result %d exact digest index contains duplicate %q", resID, dig)
			}
			seen[dig] = struct{}{}
			posting := c.egraphResultsByDigest[dig]
			if posting == nil || !posting.Contains(resID) {
				return fmt.Errorf("result %d exact digest %q is missing its posting", resID, dig)
			}
		}
		exactDigestsByResult[resID] = seen
	}

	for resID := range c.broadlyIndexedResults {
		if c.resultsByID[resID] == nil {
			return fmt.Errorf("broad digest marker references missing result %d", resID)
		}
	}

	for dig, posting := range c.egraphResultsByDigest {
		if posting == nil {
			continue
		}
		for resID := range posting.Items() {
			if c.resultsByID[resID] == nil {
				return fmt.Errorf("digest posting %q references missing result %d", dig, resID)
			}
			_, exact := exactDigestsByResult[resID][dig]
			_, broad := c.broadlyIndexedResults[resID]
			if !exact && !broad {
				return fmt.Errorf("digest posting %q result %d is neither exact-listed nor broad", dig, resID)
			}
		}
	}
	return nil
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

type cacheDerivedIndexFixture struct {
	cache        *Cache
	ctx          context.Context
	dbPath       string
	setupSession string
	allResultIDs []sharedResultID
}

func (f *cacheDerivedIndexFixture) close(t testing.TB) {
	t.Helper()
	if f == nil || f.cache == nil {
		return
	}
	assert.NilError(t, f.cache.CloseDiscardingPersistence())
	f.cache = nil
}

func newCacheDerivedIndexFixtureCache(t testing.TB, sessionID, dbPath string) (*Cache, context.Context) {
	t.Helper()
	ctx := cacheTestSessionContext(t.Context(), sessionID)
	c, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	return c, ContextWithCache(ctx, c)
}

func cacheDerivedIndexPublishPersistedResult(
	t testing.TB,
	f *cacheDerivedIndexFixture,
	requestCall, responseCall *ResultCall,
	value int,
) AnyResult {
	t.Helper()
	res, err := f.cache.GetOrInitCall(f.ctx, f.setupSession, noopTypeResolver{}, &CallRequest{
		ResultCall:    requestCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(responseCall, value), nil
	})
	assert.NilError(t, err)
	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil && shared.id != 0)
	f.allResultIDs = append(f.allResultIDs, shared.id)
	return res
}

func cacheDerivedIndexWideOutputFixture(t testing.TB, width int, imported bool) *cacheDerivedIndexFixture {
	t.Helper()
	f := &cacheDerivedIndexFixture{setupSession: "derived-index-wide-output"}
	if imported {
		f.dbPath = filepath.Join(t.TempDir(), "cache.db")
	}
	f.cache, f.ctx = newCacheDerivedIndexFixtureCache(t, f.setupSession, f.dbPath)

	sharedInputDigest := digest.FromString("derived-index-wide-input-content")
	parents := make([]AnyResult, 0, width+1)
	for i := range width + 1 {
		request := cacheTestIntCall("derived-index-wide-input-request-" + strconv.Itoa(i))
		response := cacheTestIntCall("derived-index-wide-input-response-"+strconv.Itoa(i), call.ExtraDigest{
			Label:  call.ExtraDigestLabelContent,
			Digest: sharedInputDigest,
		})
		parents = append(parents, cacheDerivedIndexPublishPersistedResult(t, f, request, response, i))
	}

	sharedOutputDigest := digest.FromString("derived-index-wide-output-content")
	for i := range width {
		publishCall := cacheTestIntCall("derived-index-wide-output-publish-" + strconv.Itoa(i))
		response := cacheTestIntCall(publishCall.Field, call.ExtraDigest{
			Label:  call.ExtraDigestLabelContent,
			Digest: sharedOutputDigest,
		})
		res := cacheDerivedIndexPublishPersistedResult(t, f, publishCall, response, i)
		structural := &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "derived-index-wide-structural",
			Receiver: &ResultCallRef{ResultID: uint64(parents[i].cacheSharedResult().id)},
		}
		assert.NilError(t, f.cache.TeachCallEquivalentToResult(f.ctx, f.setupSession, structural, res))
	}

	assert.NilError(t, f.cache.ReleaseSession(f.ctx, f.setupSession))
	if imported {
		assert.NilError(t, f.cache.Close(t.Context()))
		f.cache, f.ctx = newCacheDerivedIndexFixtureCache(t, f.setupSession, f.dbPath)
	}
	return f
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
	classCount := len(classes)
	var outputEqID eqClassID
	for outputEqID = range classes {
	}
	termCount := len(c.outputEqClassToTerms[outputEqID])
	c.egraphMu.Unlock()
	assert.Equal(t, 1, classCount)
	assert.Assert(t, termCount > 0)

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-mixed-a"))

	c.egraphMu.Lock()
	_, removedPresent := c.outputEqClassResults[outputEqID][sharedA.id]
	_, survivorPresent := c.outputEqClassResults[outputEqID][sharedB.id]
	survivingTermCount := len(c.outputEqClassToTerms[outputEqID])
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Assert(t, !removedPresent)
	assert.Assert(t, survivorPresent)
	assert.Equal(t, termCount, survivingTermCount)
	assert.NilError(t, indexErr)

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
	classCount := len(classes)
	var outputEqID eqClassID
	for outputEqID = range classes {
	}
	removedTermIDs := make([]egraphTermID, 0, len(c.outputEqClassToTerms[outputEqID]))
	for termID := range c.outputEqClassToTerms[outputEqID] {
		removedTermIDs = append(removedTermIDs, termID)
	}
	sharedB.expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()
	assert.Equal(t, 1, classCount)
	assert.Assert(t, len(removedTermIDs) > 0)

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-expired-a"))

	c.egraphMu.Lock()
	survivorPresent := c.resultsByID[sharedB.id] == sharedB
	removedTerms := make(map[egraphTermID]bool, len(removedTermIDs))
	for _, termID := range removedTermIDs {
		removedTerms[termID] = c.egraphTerms[termID] == nil
	}
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Assert(t, survivorPresent)
	for termID, removed := range removedTerms {
		assert.Assert(t, removed, "expired-only output term %d was retained", termID)
	}
	assert.NilError(t, indexErr)

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
	classCountA, classCountB := len(classesA), len(classesB)
	var rootA, rootB eqClassID
	for rootA = range classesA {
	}
	for rootB = range classesB {
	}
	distinctRoots := rootA != rootB
	// Exercise the idempotent result-already-in-both-roots merge case.
	c.addResultOutputEqClassLocked(sharedA.id, rootB)
	mergedRoot := c.mergeEqClassesLocked(baseCtx, rootA, rootB)
	mergedA := maps.Clone(c.resultOutputEqClasses[sharedA.id])
	mergedB := maps.Clone(c.resultOutputEqClasses[sharedB.id])
	mergedCount := len(c.outputEqClassResults[mergedRoot])
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Equal(t, 1, classCountA)
	assert.Equal(t, 1, classCountB)
	assert.Assert(t, distinctRoots)
	assert.Assert(t, mergedRoot != 0)
	assert.DeepEqual(t, mergedA, map[eqClassID]struct{}{mergedRoot: {}})
	assert.DeepEqual(t, mergedB, map[eqClassID]struct{}{mergedRoot: {}})
	assert.Equal(t, 2, mergedCount)
	assert.NilError(t, indexErr)

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
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Assert(t, changed)
	assert.Assert(t, oldSlots > newSlots)
	assert.NilError(t, indexErr)

	assert.NilError(t, c.ReleaseSession(ctxA, "inverse-compact-a"))
	assertCacheDerivedIndexesConsistent(t, c)
	assert.NilError(t, c.ReleaseSession(ctxB, "inverse-compact-b"))

	c.egraphMu.Lock()
	outputEqClassResultsNil := c.outputEqClassResults == nil
	resultOutputEqClassesNil := c.resultOutputEqClasses == nil
	resultIndexedDigestsNil := c.resultIndexedDigests == nil
	broadlyIndexedResultsNil := c.broadlyIndexedResults == nil
	c.egraphMu.Unlock()
	assert.Assert(t, outputEqClassResultsNil)
	assert.Assert(t, resultOutputEqClassesNil)
	assert.Assert(t, resultIndexedDigestsNil)
	assert.Assert(t, broadlyIndexedResultsNil)
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
	childOwners := child.cacheSharedResult().incomingOwnershipCount
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Equal(t, int64(2), childOwners)
	assert.NilError(t, indexErr)
	assert.NilError(t, c.ReleaseSession(ctxChild, "inverse-cascade-child"))
	assert.NilError(t, c.ReleaseSession(ctxParent, "inverse-cascade-parent"))

	assert.Assert(t, slices.Equal(callbacks, []string{"parent", "child"}), "callback order: %v", callbacks)
	assert.Equal(t, 0, c.Size())
	c.egraphMu.Lock()
	outputEqClassResultsNil := c.outputEqClassResults == nil
	c.egraphMu.Unlock()
	assert.Assert(t, outputEqClassResultsNil)
}

func TestCacheExactDigestPostingRemoval(t *testing.T) {
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctxTarget := cacheTestSessionContext(baseCtx, "exact-posting-target")
	ctxKeeper := cacheTestSessionContext(baseCtx, "exact-posting-keeper")

	requestExtra := digest.FromString("exact-posting-request-extra")
	responseExtra := digest.FromString("exact-posting-response-extra")
	requestCall := cacheTestIntCall("exact-posting-request", call.ExtraDigest{Label: "request", Digest: requestExtra})
	responseCall := cacheTestIntCall("exact-posting-response", call.ExtraDigest{Label: "response", Digest: responseExtra})
	res, err := c.GetOrInitCall(ctxTarget, "exact-posting-target", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(responseCall, 1), nil
	})
	assert.NilError(t, err)
	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)

	keeperCall := cacheTestIntCall("exact-posting-keeper")
	_, err = c.GetOrInitCall(ctxKeeper, "exact-posting-keeper", noopTypeResolver{}, &CallRequest{ResultCall: keeperCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keeperCall, 2), nil
	})
	assert.NilError(t, err)

	requestDigest, err := requestCall.deriveRecipeDigest(c)
	assert.NilError(t, err)
	responseDigest, err := responseCall.deriveRecipeDigest(c)
	assert.NilError(t, err)
	expectedDigests := []string{
		requestDigest.String(),
		requestExtra.String(),
		responseDigest.String(),
		responseExtra.String(),
	}

	c.egraphMu.Lock()
	before := len(c.resultIndexedDigests[shared.id])
	for _, dig := range expectedDigests {
		c.addResultDigestPostingLocked(shared.id, dig, resultDigestPostingExact)
		c.addResultDigestPostingLocked(shared.id, dig, resultDigestPostingExact)
	}
	after := len(c.resultIndexedDigests[shared.id])
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Equal(t, before, after)
	assert.Equal(t, len(expectedDigests), before)
	assert.NilError(t, indexErr)

	assert.NilError(t, c.ReleaseSession(ctxTarget, "exact-posting-target"))
	c.egraphMu.Lock()
	_, reversePresent := c.resultIndexedDigests[shared.id]
	removedPostings := make(map[string]bool, len(expectedDigests))
	for _, dig := range expectedDigests {
		posting := c.egraphResultsByDigest[dig]
		removedPostings[dig] = posting == nil || !posting.Contains(shared.id)
	}
	indexErr = cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.Assert(t, !reversePresent)
	for dig, removed := range removedPostings {
		assert.Assert(t, removed, "removed result %d remains posted under %q", shared.id, dig)
	}
	assert.NilError(t, indexErr)

	assert.NilError(t, c.ReleaseSession(ctxKeeper, "exact-posting-keeper"))
}

func TestCacheBroadImportedPostingRemoval(t *testing.T) {
	const width = 4
	f := cacheDerivedIndexWideOutputFixture(t, width, true)
	defer f.close(t)

	f.cache.egraphMu.Lock()
	type importedState struct {
		broad      bool
		exactCount int
	}
	imported := make(map[sharedResultID]importedState, len(f.cache.resultsByID))
	for resultID := range f.cache.resultsByID {
		_, broad := f.cache.broadlyIndexedResults[resultID]
		imported[resultID] = importedState{broad, len(f.cache.resultIndexedDigests[resultID])}
	}
	// A broad imported result may later gain an exact runtime posting. The broad
	// marker remains authoritative while the new posting is recorded exactly.
	mixedID := f.allResultIDs[0]
	mixedDigest := digest.FromString("broad-imported-later-exact").String()
	f.cache.addResultDigestPostingLocked(mixedID, mixedDigest, resultDigestPostingExact)
	_, stillBroad := f.cache.broadlyIndexedResults[mixedID]
	mixedDigests := slices.Clone(f.cache.resultIndexedDigests[mixedID])
	type fixtureState struct {
		present, persisted bool
		owners             int64
		deps               int
	}
	fixtures := make(map[sharedResultID]fixtureState, len(f.allResultIDs))
	for _, resultID := range f.allResultIDs {
		res := f.cache.resultsByID[resultID]
		_, persisted := f.cache.persistedEdgesByResult[resultID]
		state := fixtureState{present: res != nil, persisted: persisted}
		if res != nil {
			state.owners, state.deps = res.incomingOwnershipCount, len(res.deps)
		}
		fixtures[resultID] = state
	}
	for i := range 8 {
		f.cache.ensureEqClassForDigestLocked(f.ctx, fmt.Sprintf("broad-imported-dead-%d", i))
	}
	changed, oldSlots, newSlots := f.cache.compactEqClassesLocked(true)
	indexErr := cacheDerivedIndexesErrorLocked(f.cache)
	f.cache.egraphMu.Unlock()
	for resultID, state := range imported {
		assert.Assert(t, state.broad, "imported result %d is not marked broad", resultID)
		assert.Equal(t, 0, state.exactCount, "imported result %d duplicated broad postings into the exact index", resultID)
	}
	assert.Assert(t, stillBroad)
	assert.DeepEqual(t, mixedDigests, []string{mixedDigest})
	for resultID, state := range fixtures {
		assert.Assert(t, state.present, "fixture result %d is missing after import", resultID)
		assert.Assert(t, state.persisted, "fixture result %d has no persisted edge", resultID)
		assert.Equal(t, int64(1), state.owners, "fixture result %d does not have exactly one persisted owner", resultID)
		assert.Equal(t, 0, state.deps, "fixture result %d unexpectedly owns dependency edges", resultID)
	}
	assert.Assert(t, changed, "forced compaction did not run: %d -> %d", oldSlots, newSlots)
	assert.NilError(t, indexErr)

	pruneCtx := withMetadataPruneContext(f.ctx)
	for i, resultID := range f.allResultIDs {
		removed, err := f.cache.removePersistedEdge(pruneCtx, resultID)
		assert.NilError(t, err)
		assert.Assert(t, removed, "persisted edge for result %d was not removed", resultID)

		f.cache.egraphMu.Lock()
		collected := f.cache.resultsByID[resultID] == nil
		resultCount := len(f.cache.resultsByID)
		removedPostings := make(map[string]bool, len(f.cache.egraphResultsByDigest))
		for dig, posting := range f.cache.egraphResultsByDigest {
			removedPostings[dig] = posting == nil || !posting.Contains(resultID)
		}
		indexErr := cacheDerivedIndexesErrorLocked(f.cache)
		f.cache.egraphMu.Unlock()
		assert.Assert(t, collected, "result %d survived removal of its only owner", resultID)
		assert.Equal(t, len(f.allResultIDs)-i-1, resultCount, "cutting result %d collected an unexpected result set", resultID)
		for dig, removed := range removedPostings {
			assert.Assert(t, removed, "removed broad result %d remains posted under %q", resultID, dig)
		}
		assert.NilError(t, indexErr)
	}

	f.cache.egraphMu.Lock()
	egraphResultsByDigestNil := f.cache.egraphResultsByDigest == nil
	resultIndexedDigestsNil := f.cache.resultIndexedDigests == nil
	broadlyIndexedResultsNil := f.cache.broadlyIndexedResults == nil
	outputEqClassResultsNil := f.cache.outputEqClassResults == nil
	f.cache.egraphMu.Unlock()
	assert.Assert(t, egraphResultsByDigestNil)
	assert.Assert(t, resultIndexedDigestsNil)
	assert.Assert(t, broadlyIndexedResultsNil)
	assert.Assert(t, outputEqClassResultsNil)
}

func TestCachePersistedFreshPruneCleansDerivedIndexes(t *testing.T) {
	const width = 4
	f := cacheDerivedIndexWideOutputFixture(t, width, false)
	defer f.close(t)

	f.cache.egraphMu.Lock()
	type freshState struct {
		exactCount int
		broad      bool
	}
	fresh := make(map[sharedResultID]freshState, len(f.cache.resultsByID))
	for resultID := range f.cache.resultsByID {
		_, broad := f.cache.broadlyIndexedResults[resultID]
		fresh[resultID] = freshState{len(f.cache.resultIndexedDigests[resultID]), broad}
	}
	indexErr := cacheDerivedIndexesErrorLocked(f.cache)
	f.cache.egraphMu.Unlock()
	for resultID, state := range fresh {
		assert.Assert(t, state.exactCount > 0, "persisted-fresh result %d has no exact digest postings", resultID)
		assert.Assert(t, !state.broad, "persisted-fresh result %d is marked broad", resultID)
	}
	assert.NilError(t, indexErr)

	report, err := f.cache.PruneMetadataEstimate(f.ctx, 2, 1)
	assert.NilError(t, err)
	assert.Assert(t, report.Triggered)
	assert.Equal(t, len(f.allResultIDs), report.RemovedPersistedRootCount)
	f.cache.egraphMu.Lock()
	egraphResultsByDigestNil := f.cache.egraphResultsByDigest == nil
	resultIndexedDigestsNil := f.cache.resultIndexedDigests == nil
	broadlyIndexedResultsNil := f.cache.broadlyIndexedResults == nil
	outputEqClassResultsNil := f.cache.outputEqClassResults == nil
	f.cache.egraphMu.Unlock()
	assert.Assert(t, egraphResultsByDigestNil)
	assert.Assert(t, resultIndexedDigestsNil)
	assert.Assert(t, broadlyIndexedResultsNil)
	assert.Assert(t, outputEqClassResultsNil)
}
