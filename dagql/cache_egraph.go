package dagql

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

type eqClassID uint64
type egraphTermID uint64

type egraphTerm struct {
	id egraphTermID

	selfDigest         digest.Digest
	inputEqIDs         []eqClassID
	outputEqID         eqClassID
	termDigest         string
	outputExtraDigests []call.ExtraDigest

	result *sharedResult
}

type eqMergePair struct {
	a eqClassID
	b eqClassID
}

func calcEgraphTermDigest(selfDigest digest.Digest, inputEqIDs []eqClassID) string {
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputEqIDs {
		h = h.WithDelim().
			WithString(strconv.FormatUint(uint64(in), 10))
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
	outputExtraDigests []call.ExtraDigest,
	res *sharedResult,
) *egraphTerm {
	return &egraphTerm{
		id:                 id,
		selfDigest:         selfDigest,
		inputEqIDs:         inputEqIDs,
		outputEqID:         outputEqID,
		termDigest:         calcEgraphTermDigest(selfDigest, inputEqIDs),
		outputExtraDigests: slices.Clone(outputExtraDigests),
		result:             res,
	}
}

func mergeExtraDigestFacts(existing []call.ExtraDigest, learned []call.ExtraDigest) []call.ExtraDigest {
	if len(learned) == 0 {
		return slices.Clone(existing)
	}
	out := slices.Clone(existing)
	seen := make(map[string]struct{}, len(existing))
	for _, extra := range out {
		if extra.Digest == "" {
			continue
		}
		seen[extra.Digest.String()+"\x00"+extra.Label] = struct{}{}
	}
	for _, extra := range learned {
		if extra.Digest == "" {
			continue
		}
		key := extra.Digest.String() + "\x00" + extra.Label
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, extra)
	}
	return out
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
	if c.egraphTermsByOutputDigest == nil {
		c.egraphTermsByOutputDigest = make(map[string]map[egraphTermID]struct{})
	}
	if c.egraphResultTerms == nil {
		c.egraphResultTerms = make(map[*sharedResult]map[egraphTermID]struct{})
	}
	if c.nextEgraphClassID == 0 {
		c.nextEgraphClassID = 1
	}
	if c.nextEgraphTermID == 0 {
		c.nextEgraphTermID = 1
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
	for termID := range termSet {
		term := c.egraphTerms[termID]
		if term == nil || term.result == nil {
			continue
		}
		return term
	}
	return nil
}

func (c *cache) indexTermOutputDigestsLocked(term *egraphTerm) {
	if term == nil || term.result == nil {
		return
	}
	c.initEgraphLocked()

	indexDigest := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		set := c.egraphTermsByOutputDigest[dig.String()]
		if set == nil {
			set = make(map[egraphTermID]struct{})
			c.egraphTermsByOutputDigest[dig.String()] = set
		}
		set[term.id] = struct{}{}
	}

	indexDigest(term.result.outputDigest)
	for _, extra := range term.outputExtraDigests {
		indexDigest(extra.Digest)
	}
}

func (c *cache) removeTermOutputDigestsLocked(term *egraphTerm) {
	if term == nil || term.result == nil {
		return
	}

	removeDigest := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		set := c.egraphTermsByOutputDigest[dig.String()]
		if set == nil {
			return
		}
		delete(set, term.id)
		if len(set) == 0 {
			delete(c.egraphTermsByOutputDigest, dig.String())
		}
	}

	removeDigest(term.result.outputDigest)
	for _, extra := range term.outputExtraDigests {
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
	}

	if hitTerm == nil {
		for _, extra := range id.ExtraDigests() {
			if extra.Digest == "" {
				continue
			}
			termSet := c.egraphTermsByOutputDigest[extra.Digest.String()]
			hitTerm = c.firstLiveTermInSetLocked(termSet)
			if hitTerm != nil {
				break
			}
		}
	}

	if hitTerm == nil {
		return nil, false, nil
	}

	// We have a cache hit, make sure that the requested ID digest is in the same eq class as the
	// cached result's output digest, and if not, merge them since we know them now to be equivalent
	res := hitTerm.result
	atomic.AddInt64(&res.refCount, 1)

	requestEqID := c.ensureEqClassForDigestLocked(id.Digest().String())
	mergeIDs := make([]eqClassID, 0, 2+len(id.ExtraDigests()))
	mergeIDs = append(mergeIDs, hitTerm.outputEqID, requestEqID)
	for _, extra := range id.ExtraDigests() {
		if extra.Digest == "" {
			continue
		}
		mergeIDs = append(mergeIDs, c.ensureEqClassForDigestLocked(extra.Digest.String()))
	}
	mergedOutputEqID := c.mergeEqClassesLocked(mergeIDs...)
	if mergedOutputEqID != 0 {
		hitTerm.outputEqID = mergedOutputEqID
	}

	// Materialize caller-facing result preserving request recipe identity.
	retID := id
	for _, extra := range hitTerm.outputExtraDigests {
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

func (c *cache) resultTermByDigestLocked(res *sharedResult, termDigest string) *egraphTerm {
	termSet := c.egraphResultTerms[res]
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
	res *sharedResult,
	resWasCacheBacked bool,
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

	termsToIndex := []struct {
		selfDigest   digest.Digest
		inputDigests []digest.Digest
	}{
		{
			selfDigest:   requestSelf,
			inputDigests: requestInputs,
		},
	}
	shouldIndexResultTerm := res.hasResultTerm && !resWasCacheBacked
	if shouldIndexResultTerm && requestSelf == res.resultTermSelf && slices.Equal(requestInputs, res.resultTermInputs) {
		shouldIndexResultTerm = false
	}
	if shouldIndexResultTerm {
		termsToIndex = append(termsToIndex, struct {
			selfDigest   digest.Digest
			inputDigests []digest.Digest
		}{
			selfDigest:   res.resultTermSelf,
			inputDigests: res.resultTermInputs,
		})
	}

	for _, term := range termsToIndex {
		inputEqIDs := c.ensureTermInputEqIDsLocked(term.inputDigests)
		termDigest := calcEgraphTermDigest(term.selfDigest, inputEqIDs)

		if existingTerm := c.resultTermByDigestLocked(res, termDigest); existingTerm != nil {
			existingTerm.outputExtraDigests = mergeExtraDigestFacts(existingTerm.outputExtraDigests, res.outputExtraDigests)
			c.indexTermOutputDigestsLocked(existingTerm)
			c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)
			continue
		}

		mergedOutputEqID := c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)

		c.initEgraphLocked()
		termID := c.nextEgraphTermID
		c.nextEgraphTermID++

		newTerm := newEgraphTerm(termID, term.selfDigest, inputEqIDs, mergedOutputEqID, res.outputExtraDigests, res)
		c.egraphTerms[termID] = newTerm
		c.indexTermOutputDigestsLocked(newTerm)

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

		resultTerms := c.egraphResultTerms[res]
		if resultTerms == nil {
			resultTerms = make(map[egraphTermID]struct{})
			c.egraphResultTerms[res] = resultTerms
		}
		resultTerms[termID] = struct{}{}
	}
}

func (c *cache) removeResultFromEgraphLocked(res *sharedResult) {
	if res == nil {
		return
	}
	if len(c.egraphTerms) == 0 || len(c.egraphResultTerms) == 0 {
		c.maybeResetEgraphLocked()
		return
	}

	termSet := c.egraphResultTerms[res]
	for termID := range termSet {
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
		c.removeTermOutputDigestsLocked(term)
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
	delete(c.egraphResultTerms, res)
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
	c.egraphTermsByOutputDigest = nil
	c.egraphResultTerms = nil
	c.nextEgraphClassID = 0
	c.nextEgraphTermID = 0
}
