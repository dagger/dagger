package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

type eqClassID uint64
type egraphTermID uint64

// egraphTerm is purely symbolic: operation shape + canonicalized input/output
// equivalence state.
//
// Materialized payload ownership and output-digest indexing live on sharedResult
// and cache-level association maps.
type egraphTerm struct {
	// id is a unique immutable identifier for this term instance.
	// It is never rewritten during eq-class merges/repair.
	id egraphTermID

	// selfDigest identifies the operation shape itself, excluding input IDs.
	// Example: for withExec it includes exec args, but not input mount identities.
	selfDigest digest.Digest

	// inputEqIDs are the canonical eq-class IDs of this term's inputs.
	// These may be rewritten as classes merge and terms are repaired.
	inputEqIDs []eqClassID

	// termDigest is the memoized digest of (selfDigest + canonical inputEqIDs).
	// It is used for congruence checks ("same op over equivalent inputs").
	// This may be recomputed as inputEqIDs are repaired.
	termDigest string

	// outputEqID is the canonical eq-class ID for this term's output.
	// This may change as output classes are merged.
	outputEqID eqClassID
}

type eqMergePair struct {
	a eqClassID
	b eqClassID
}

func calcEgraphTermDigest(selfDigest digest.Digest, inputEqIDs []eqClassID) string {
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputEqIDs {
		h = h.WithDelim().
			WithUint64(uint64(in))
	}
	return h.DigestAndClose()
}

func (c *cache) ensureTermInputEqIDsLocked(inputDigests []digest.Digest) []eqClassID {
	inputEqIDs := make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		inputEqIDs[i] = c.findEqClassLocked(c.ensureEqClassForDigestLocked(inDig.String()))
	}
	return inputEqIDs
}

func newEgraphTerm(
	id egraphTermID,
	selfDigest digest.Digest,
	inputEqIDs []eqClassID,
	outputEqID eqClassID,
) *egraphTerm {
	return &egraphTerm{
		id:         id,
		selfDigest: selfDigest,
		inputEqIDs: inputEqIDs,
		outputEqID: outputEqID,
		termDigest: calcEgraphTermDigest(selfDigest, inputEqIDs),
	}
}

func (c *cache) initEgraphLocked() {
	if c.egraphDigestToClass == nil {
		c.egraphDigestToClass = make(map[string]eqClassID)
	}
	if c.egraphParents == nil {
		// index 0 is reserved as "unset"
		c.egraphParents = []eqClassID{0}
	}
	if c.egraphRanks == nil {
		c.egraphRanks = []uint8{0}
	}
	if c.egraphClassTerms == nil {
		c.egraphClassTerms = make(map[eqClassID]map[egraphTermID]struct{})
	}
	if c.egraphTerms == nil {
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
	}
	if c.egraphTermsByDigest == nil {
		c.egraphTermsByDigest = make(map[string]map[egraphTermID]struct{})
	}
	if c.egraphResultsByOutputDigest == nil {
		c.egraphResultsByOutputDigest = make(map[string]map[sharedResultID]struct{})
	}
	if c.egraphResultsByTermID == nil {
		c.egraphResultsByTermID = make(map[egraphTermID]map[sharedResultID]struct{})
	}
	if c.egraphTermIDsByResult == nil {
		c.egraphTermIDsByResult = make(map[sharedResultID]map[egraphTermID]struct{})
	}
	if c.resultsByID == nil {
		c.resultsByID = make(map[sharedResultID]*sharedResult)
	}
	if c.nextEgraphClassID == 0 {
		c.nextEgraphClassID = 1
	}
	if c.nextEgraphTermID == 0 {
		c.nextEgraphTermID = 1
	}
	if c.nextSharedResultID == 0 {
		c.nextSharedResultID = 1
	}
}

func (c *cache) ensureEqClassForDigestLocked(dig string) eqClassID {
	if dig == "" {
		return 0
	}
	c.initEgraphLocked()
	if id, ok := c.egraphDigestToClass[dig]; ok {
		return c.findEqClassLocked(id)
	}
	id := c.nextEgraphClassID
	c.nextEgraphClassID++
	c.egraphParents = append(c.egraphParents, id)
	c.egraphRanks = append(c.egraphRanks, 0)
	c.egraphDigestToClass[dig] = id
	return id
}

func (c *cache) findEqClassLocked(id eqClassID) eqClassID {
	if id == 0 || int(id) >= len(c.egraphParents) {
		return 0
	}
	root := id
	for c.egraphParents[root] != root {
		root = c.egraphParents[root]
	}
	for id != root {
		parent := c.egraphParents[id]
		c.egraphParents[id] = root
		id = parent
	}
	return root
}

func (c *cache) mergeEqClassesNoRepairLocked(a, b eqClassID) eqClassID {
	ra := c.findEqClassLocked(a)
	rb := c.findEqClassLocked(b)
	if ra == 0 {
		return rb
	}
	if rb == 0 {
		return ra
	}
	if ra == rb {
		return ra
	}

	// union by rank
	if c.egraphRanks[ra] < c.egraphRanks[rb] {
		ra, rb = rb, ra
	}
	c.egraphParents[rb] = ra
	if c.egraphRanks[ra] == c.egraphRanks[rb] {
		c.egraphRanks[ra]++
	}

	// merge reverse input-term indexes
	dst := c.egraphClassTerms[ra]
	src := c.egraphClassTerms[rb]
	if len(src) > 0 {
		if dst == nil {
			dst = make(map[egraphTermID]struct{}, len(src))
			c.egraphClassTerms[ra] = dst
		}
		for termID := range src {
			dst[termID] = struct{}{}
		}
		delete(c.egraphClassTerms, rb)
	}

	return ra
}

func (c *cache) mergeEqClassesLocked(ids ...eqClassID) eqClassID {
	if len(ids) == 0 {
		return 0
	}
	if len(ids) == 1 {
		return c.findEqClassLocked(ids[0])
	}

	root := c.findEqClassLocked(ids[0])
	for _, id := range ids[1:] {
		root = c.mergeEqClassesNoRepairLocked(root, id)
	}

	toRepair := []eqClassID{root}
	repaired := make(map[eqClassID]struct{})
	for len(toRepair) > 0 {
		cur := c.findEqClassLocked(toRepair[len(toRepair)-1])
		toRepair = toRepair[:len(toRepair)-1]
		if cur == 0 {
			continue
		}
		if _, ok := repaired[cur]; ok {
			continue
		}
		repaired[cur] = struct{}{}
		for _, pair := range c.repairClassTermsLocked(cur) {
			next := c.mergeEqClassesNoRepairLocked(pair.a, pair.b)
			if next != 0 {
				toRepair = append(toRepair, next)
			}
		}
	}
	return c.findEqClassLocked(ids[0])
}

func (c *cache) repairClassTermsLocked(root eqClassID) (merges []eqMergePair) {
	termSet := c.egraphClassTerms[root]
	if len(termSet) == 0 {
		return nil
	}

	termIDs := make([]egraphTermID, 0, len(termSet))
	for termID := range termSet {
		termIDs = append(termIDs, termID)
	}

	for _, termID := range termIDs {
		term := c.egraphTerms[termID]
		if term == nil {
			delete(termSet, termID)
			continue
		}

		oldInputs := term.inputEqIDs
		newInputs := make([]eqClassID, len(oldInputs))
		for i, in := range oldInputs {
			rootIn := c.findEqClassLocked(in)
			newInputs[i] = rootIn
		}
		inputsChanged := !slices.Equal(newInputs, oldInputs)

		if inputsChanged {
			// Re-home this term under canonical input classes.
			for _, in := range oldInputs {
				if set := c.egraphClassTerms[in]; set != nil {
					delete(set, termID)
					if len(set) == 0 {
						delete(c.egraphClassTerms, in)
					}
				}
			}
			for _, in := range newInputs {
				if in == 0 {
					continue
				}
				set := c.egraphClassTerms[in]
				if set == nil {
					set = make(map[egraphTermID]struct{})
					c.egraphClassTerms[in] = set
				}
				set[termID] = struct{}{}
			}
			term.inputEqIDs = newInputs
		}

		newTermDigest := calcEgraphTermDigest(term.selfDigest, term.inputEqIDs)
		if newTermDigest != term.termDigest {
			if set := c.egraphTermsByDigest[term.termDigest]; set != nil {
				delete(set, termID)
				if len(set) == 0 {
					delete(c.egraphTermsByDigest, term.termDigest)
				}
			}
			set := c.egraphTermsByDigest[newTermDigest]
			if set == nil {
				set = make(map[egraphTermID]struct{})
				c.egraphTermsByDigest[newTermDigest] = set
			}
			set[termID] = struct{}{}
			term.termDigest = newTermDigest
		}

		set := c.egraphTermsByDigest[term.termDigest]
		if len(set) <= 1 {
			continue
		}

		var first *egraphTerm
		for otherID := range set {
			other := c.egraphTerms[otherID]
			if other == nil {
				continue
			}
			if first == nil || other.id < first.id {
				first = other
			}
		}
		if first == nil {
			continue
		}
		for otherID := range set {
			other := c.egraphTerms[otherID]
			if other == nil || other.id == first.id {
				continue
			}
			merges = append(merges, eqMergePair{
				a: first.outputEqID,
				b: other.outputEqID,
			})
		}
	}
	return merges
}

func (c *cache) firstLiveTermInSetLocked(termSet map[egraphTermID]struct{}) *egraphTerm {
	var best *egraphTerm
	for termID := range termSet {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		if best == nil || term.id < best.id {
			best = term
		}
	}
	return best
}

func (c *cache) firstResultDeterministicallyLocked(resultSet map[sharedResultID]struct{}) *sharedResult {
	return c.firstResultDeterministicallyAtLocked(resultSet, time.Now().Unix())
}

func (c *cache) firstResultDeterministicallyAtLocked(
	resultSet map[sharedResultID]struct{},
	nowUnix int64,
) *sharedResult {
	var bestID sharedResultID
	for resID := range resultSet {
		res := c.resultsByID[resID]
		if res == nil {
			continue
		}
		if c.resultExpiredAtLocked(res, nowUnix) {
			continue
		}
		if bestID == 0 || resID < bestID {
			bestID = resID
		}
	}
	return c.resultsByID[bestID]
}

func (c *cache) firstResultForTermSetDeterministicallyLocked(termSet map[egraphTermID]struct{}) *sharedResult {
	return c.firstResultForTermSetDeterministicallyAtLocked(termSet, time.Now().Unix())
}

func (c *cache) firstResultForTermSetDeterministicallyAtLocked(
	termSet map[egraphTermID]struct{},
	nowUnix int64,
) *sharedResult {
	var bestID sharedResultID
	for termID := range termSet {
		resultSet := c.egraphResultsByTermID[termID]
		res := c.firstResultDeterministicallyAtLocked(resultSet, nowUnix)
		if res == nil {
			continue
		}
		if bestID == 0 || res.id < bestID {
			bestID = res.id
		}
	}
	return c.resultsByID[bestID]
}

func (c *cache) resultExpiredAtLocked(res *sharedResult, nowUnix int64) bool {
	if res == nil || res.expiresAtUnix == 0 {
		return false
	}
	return nowUnix >= res.expiresAtUnix
}

func (c *cache) firstTermForResultLocked(resID sharedResultID) *egraphTerm {
	termSet := c.egraphTermIDsByResult[resID]
	return c.firstLiveTermInSetLocked(termSet)
}

func (c *cache) indexResultOutputDigestsLocked(res *sharedResult) {
	if res == nil {
		return
	}
	c.initEgraphLocked()

	indexDigest := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		set := c.egraphResultsByOutputDigest[dig.String()]
		if set == nil {
			set = make(map[sharedResultID]struct{})
			c.egraphResultsByOutputDigest[dig.String()] = set
		}
		set[res.id] = struct{}{}
	}

	indexDigest(res.outputDigest)
	for _, extra := range res.outputExtraDigests {
		indexDigest(extra.Digest)
	}
}

func (c *cache) removeResultOutputDigestsLocked(res *sharedResult) {
	if res == nil {
		return
	}

	removeDigest := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		set := c.egraphResultsByOutputDigest[dig.String()]
		if set == nil {
			return
		}
		delete(set, res.id)
		if len(set) == 0 {
			delete(c.egraphResultsByOutputDigest, dig.String())
		}
	}

	removeDigest(res.outputDigest)
	for _, extra := range res.outputExtraDigests {
		removeDigest(extra.Digest)
	}
}

// lookupCacheForID checks if the given call ID has an equivalent result in the cache. It first
// attempts the canonical term lookup using (self, input eq-classes). If that misses, it can
// fall back to matching the request's extra digests against known output digests.
//
// This method assumes egraphMu is already held by the caller.
func (c *cache) lookupCacheForID(
	_ context.Context,
	id *call.ID,
	persistable bool,
	ttlSeconds int64,
) (AnyResult, bool, error) {
	// (self digest, input eqSet IDs) are digested to create the "real" cache key we do a lookup on.
	// Figure those out first.
	if id == nil {
		return nil, false, nil
	}
	selfDigest, inputDigests, err := id.SelfDigestAndInputs()
	if err != nil {
		return nil, false, fmt.Errorf("derive call term: %w", err)
	}

	var (
		inputEqIDs            []eqClassID
		primaryLookupPossible = true
		hitTerm               *egraphTerm
		hitRes                *sharedResult
		nowUnix               = time.Now().Unix()
	)
	inputEqIDs = make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		classID, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			primaryLookupPossible = false
			break
		}
		root := c.findEqClassLocked(classID)
		if root == 0 {
			primaryLookupPossible = false
			break
		}
		inputEqIDs[i] = root
	}
	if primaryLookupPossible {
		termDigest := calcEgraphTermDigest(selfDigest, inputEqIDs)
		termSet := c.egraphTermsByDigest[termDigest]
		hitTerm = c.firstLiveTermInSetLocked(termSet)
		hitRes = c.firstResultForTermSetDeterministicallyAtLocked(termSet, nowUnix)
	}

	if hitRes == nil {
		// Fallback path: resolve directly to results by output digest (primary
		// output digest or extra digests), then pick any associated term (if one
		// exists) so eq-class merge updates can still be applied.
		for _, extra := range id.ExtraDigests() {
			if extra.Digest == "" {
				continue
			}
			resultSet := c.egraphResultsByOutputDigest[extra.Digest.String()]
			hitRes = c.firstResultDeterministicallyAtLocked(resultSet, nowUnix)
			if hitRes != nil {
				hitTerm = c.firstTermForResultLocked(hitRes.id)
				break
			}
		}
	}

	if hitRes == nil {
		return nil, false, nil
	}

	// We have a cache hit, make sure that the requested ID digest is in the same eq class as the
	// cached result's output digest, and if not, merge them since we know them now to be equivalent
	res := hitRes
	// A TTL-bearing call can alias an existing result on lookup; apply the same
	// conservative expiry merge policy here so TTL remains effective on hits.
	res.expiresAtUnix = mergeSharedResultExpiryUnix(
		res.expiresAtUnix,
		candidateSharedResultExpiryUnix(nowUnix, ttlSeconds),
	)
	if persistable {
		// NOTE: this is an intentional experiment behavior. If a persistable field
		// hits a result originally produced by a non-persistable field, we
		// "upgrade" the shared result to persisted-dependency liveness so future
		// releases do not drop it or its dependency chain. This avoids surprising
		// misses for persistable callsites, but should be revisited when real
		// persistence policy is finalized.
		c.markResultAsDepOfPersistedLocked(res)
	}
	atomic.AddInt64(&res.refCount, 1)

	requestEqID := c.ensureEqClassForDigestLocked(id.Digest().String())
	mergeIDs := make([]eqClassID, 0, 1+len(id.ExtraDigests()))
	mergeIDs = append(mergeIDs, requestEqID)
	if hitTerm != nil {
		mergeIDs = append(mergeIDs, hitTerm.outputEqID)
	}
	for _, extra := range id.ExtraDigests() {
		if extra.Digest == "" {
			continue
		}
		mergeIDs = append(mergeIDs, c.ensureEqClassForDigestLocked(extra.Digest.String()))
	}
	mergedOutputEqID := c.mergeEqClassesLocked(mergeIDs...)
	if mergedOutputEqID != 0 && hitTerm != nil {
		hitTerm.outputEqID = mergedOutputEqID
	}

	// Materialize caller-facing result preserving request recipe identity.
	retID := id
	for _, extra := range res.outputExtraDigests {
		if extra.Digest == "" {
			continue
		}
		retID = retID.With(call.WithExtraDigest(extra))
	}
	retID = retID.AppendEffectIDs(res.outputEffectIDs...)
	if !res.hasValue {
		return Result[Typed]{
			shared:   res,
			id:       retID,
			hitCache: true,
		}, true, nil
	}
	retRes := Result[Typed]{
		shared:   res,
		id:       retID,
		hitCache: true,
	}
	if res.objType == nil {
		return retRes, true, nil
	}
	retObjRes, err := res.objType.New(retRes)
	if err != nil {
		return nil, false, fmt.Errorf("reconstruct structural-hit object result from cache: %w", err)
	}
	return retObjRes, true, nil
}

func (c *cache) termForResultByDigestLocked(resID sharedResultID, termDigest string) *egraphTerm {
	termSet := c.egraphTermIDsByResult[resID]
	for termID := range termSet {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		if term.termDigest == termDigest {
			return term
		}
	}
	return nil
}

func (c *cache) mergeOutputsForTermDigestLocked(termDigest string, outputEqID eqClassID) eqClassID {
	set := c.egraphTermsByDigest[termDigest]
	if len(set) == 0 {
		return c.findEqClassLocked(outputEqID)
	}

	mergeIDs := []eqClassID{outputEqID}
	for termID := range set {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		mergeIDs = append(mergeIDs, term.outputEqID)
	}
	root := c.mergeEqClassesLocked(mergeIDs...)
	for termID := range set {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		term.outputEqID = root
	}
	return root
}

func (c *cache) indexWaitResultInEgraphLocked(
	requestID *call.ID,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	resultTermSelf digest.Digest,
	resultTermInputs []digest.Digest,
	hasResultTerm bool,
	res *sharedResult,
) {
	digestSet := make(map[string]struct{}, 6)
	addDigest := func(dig string) {
		if dig == "" {
			return
		}
		digestSet[dig] = struct{}{}
	}

	addDigest(requestID.Digest().String())
	for _, extra := range requestID.ExtraDigests() {
		addDigest(extra.Digest.String())
	}
	addDigest(res.outputDigest.String())
	for _, extra := range res.outputExtraDigests {
		addDigest(extra.Digest.String())
	}
	if len(digestSet) == 0 {
		return
	}

	rootSet := make(map[eqClassID]struct{}, len(digestSet))
	for dig := range digestSet {
		if id := c.ensureEqClassForDigestLocked(dig); id != 0 {
			rootSet[id] = struct{}{}
		}
	}
	if len(rootSet) == 0 {
		return
	}

	var outputEqID eqClassID
	if len(rootSet) == 1 {
		for root := range rootSet {
			outputEqID = root
		}
	} else {
		mergeIDs := make([]eqClassID, 0, len(rootSet))
		for root := range rootSet {
			mergeIDs = append(mergeIDs, root)
		}
		outputEqID = c.mergeEqClassesLocked(mergeIDs...)
	}
	if outputEqID == 0 {
		return
	}
	c.initEgraphLocked()
	if res.id == 0 {
		res.id = c.nextSharedResultID
		c.nextSharedResultID++
	}
	c.resultsByID[res.id] = res
	c.indexResultOutputDigestsLocked(res)

	termsToIndex := []struct {
		selfDigest   digest.Digest
		inputDigests []digest.Digest
	}{
		{
			selfDigest:   requestSelf,
			inputDigests: requestInputs,
		},
	}
	shouldIndexResultTerm := hasResultTerm
	if shouldIndexResultTerm && requestSelf == resultTermSelf && slices.Equal(requestInputs, resultTermInputs) {
		shouldIndexResultTerm = false
	}
	if shouldIndexResultTerm {
		termsToIndex = append(termsToIndex, struct {
			selfDigest   digest.Digest
			inputDigests []digest.Digest
		}{
			selfDigest:   resultTermSelf,
			inputDigests: resultTermInputs,
		})
	}

	associateResultWithTerm := func(termID egraphTermID) {
		resultTerms := c.egraphTermIDsByResult[res.id]
		if resultTerms == nil {
			resultTerms = make(map[egraphTermID]struct{})
			c.egraphTermIDsByResult[res.id] = resultTerms
		}
		resultTerms[termID] = struct{}{}

		termResults := c.egraphResultsByTermID[termID]
		if termResults == nil {
			termResults = make(map[sharedResultID]struct{})
			c.egraphResultsByTermID[termID] = termResults
		}
		termResults[res.id] = struct{}{}
	}

	for _, term := range termsToIndex {
		inputEqIDs := c.ensureTermInputEqIDsLocked(term.inputDigests)
		c.indexResultDependenciesLocked(res, inputEqIDs)
		termDigest := calcEgraphTermDigest(term.selfDigest, inputEqIDs)

		if existingTerm := c.termForResultByDigestLocked(res.id, termDigest); existingTerm != nil {
			c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)
			continue
		}
		if existingTerm := c.firstLiveTermInSetLocked(c.egraphTermsByDigest[termDigest]); existingTerm != nil {
			associateResultWithTerm(existingTerm.id)
			c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)
			continue
		}

		mergedOutputEqID := c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)

		c.initEgraphLocked()
		termID := c.nextEgraphTermID
		c.nextEgraphTermID++

		newTerm := newEgraphTerm(
			termID,
			term.selfDigest,
			inputEqIDs,
			mergedOutputEqID,
		)
		c.egraphTerms[termID] = newTerm

		digestTerms := c.egraphTermsByDigest[newTerm.termDigest]
		if digestTerms == nil {
			digestTerms = make(map[egraphTermID]struct{})
			c.egraphTermsByDigest[newTerm.termDigest] = digestTerms
		}
		digestTerms[termID] = struct{}{}

		for _, inEqID := range newTerm.inputEqIDs {
			if inEqID == 0 {
				continue
			}
			classTerms := c.egraphClassTerms[inEqID]
			if classTerms == nil {
				classTerms = make(map[egraphTermID]struct{})
				c.egraphClassTerms[inEqID] = classTerms
			}
			classTerms[termID] = struct{}{}
		}

		associateResultWithTerm(termID)
	}
	if res.depOfPersistedResult {
		c.markResultAsDepOfPersistedLocked(res)
	}
}

func (c *cache) firstLiveResultForOutputEqClassLocked(outputEqID eqClassID, nowUnix int64) *sharedResult {
	outputEqID = c.findEqClassLocked(outputEqID)
	if outputEqID == 0 {
		return nil
	}
	var bestID sharedResultID
	for _, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		if c.findEqClassLocked(term.outputEqID) != outputEqID {
			continue
		}
		res := c.firstResultDeterministicallyAtLocked(c.egraphResultsByTermID[term.id], nowUnix)
		if res == nil {
			continue
		}
		if bestID == 0 || res.id < bestID {
			bestID = res.id
		}
	}
	return c.resultsByID[bestID]
}

func (c *cache) indexResultDependenciesLocked(res *sharedResult, inputEqIDs []eqClassID) {
	if res == nil {
		return
	}
	if res.deps == nil {
		res.deps = make(map[sharedResultID]struct{})
	}
	nowUnix := time.Now().Unix()
	for _, inputEqID := range inputEqIDs {
		if inputEqID == 0 {
			continue
		}
		dep := c.firstLiveResultForOutputEqClassLocked(inputEqID, nowUnix)
		if dep == nil || dep.id == res.id {
			continue
		}
		res.deps[dep.id] = struct{}{}
	}
}

func (c *cache) markResultAsDepOfPersistedLocked(root *sharedResult) {
	if root == nil {
		return
	}
	if root.id == 0 {
		root.depOfPersistedResult = true
		return
	}
	stack := []sharedResultID{root.id}
	seen := make(map[sharedResultID]struct{})
	for len(stack) > 0 {
		n := len(stack) - 1
		curID := stack[n]
		stack = stack[:n]
		cur := c.resultsByID[curID]
		if cur == nil {
			continue
		}
		if _, ok := seen[curID]; ok {
			continue
		}
		seen[curID] = struct{}{}
		cur.depOfPersistedResult = true
		for depID := range cur.deps {
			stack = append(stack, depID)
		}
	}
}

func (c *cache) removeResultFromEgraphLocked(res *sharedResult) {
	if res == nil {
		return
	}
	if len(c.egraphTerms) == 0 || len(c.egraphTermIDsByResult) == 0 {
		c.maybeResetEgraphLocked()
		return
	}

	c.removeResultOutputDigestsLocked(res)
	termSet := c.egraphTermIDsByResult[res.id]
	for termID := range termSet {
		termResults := c.egraphResultsByTermID[termID]
		delete(termResults, res.id)
		if len(termResults) > 0 {
			continue
		}
		delete(c.egraphResultsByTermID, termID)

		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		if set := c.egraphTermsByDigest[term.termDigest]; set != nil {
			delete(set, termID)
			if len(set) == 0 {
				delete(c.egraphTermsByDigest, term.termDigest)
			}
		}
		for _, in := range term.inputEqIDs {
			if set := c.egraphClassTerms[in]; set != nil {
				delete(set, termID)
				if len(set) == 0 {
					delete(c.egraphClassTerms, in)
				}
			}
		}
		delete(c.egraphTerms, termID)
	}
	delete(c.egraphTermIDsByResult, res.id)
	delete(c.resultsByID, res.id)
	c.maybeResetEgraphLocked()
}

func (c *cache) maybeResetEgraphLocked() {
	if len(c.egraphTerms) != 0 {
		return
	}

	c.egraphDigestToClass = nil
	c.egraphParents = nil
	c.egraphRanks = nil
	c.egraphClassTerms = nil
	c.egraphTerms = nil
	c.egraphTermsByDigest = nil
	c.egraphResultsByOutputDigest = nil
	c.egraphResultsByTermID = nil
	c.egraphTermIDsByResult = nil
	c.resultsByID = nil
	c.nextEgraphClassID = 0
	c.nextEgraphTermID = 0
	c.nextSharedResultID = 0
}
