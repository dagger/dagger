package dagql

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
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

// lookupCacheForID checks if the given call ID has an equivalent result in the cache. If so,
// it returns that result. It may also update the e-graph with a new known digest if this is
// the first time we've seen this call ID digest resolve to this cached result.
//
// This method assumes c.mu is already held by the caller.
func (c *cache) lookupCacheForID(
	ctx context.Context,
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
	inputEqIDs := make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		classID, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			return nil, false, nil
		}
		root := c.findEqClassLocked(classID)
		if root == 0 {
			return nil, false, nil
		}
		inputEqIDs[i] = root
	}
	termDigest := calcEgraphTermDigest(selfDigest, inputEqIDs)

	// Lookup whether we have a cache hit
	termSet := c.egraphTermsByDigest[termDigest]
	if len(termSet) == 0 {
		return nil, false, nil
	}
	var hitTerm *egraphTerm
	for termID := range termSet {
		term := c.egraphTerms[termID]
		if term == nil || term.result == nil {
			continue
		}
		hitTerm = term
		break
	}
	if hitTerm == nil {
		// no hit
		return nil, false, nil
	}

	// We have a cache hit, make sure that the requested ID digest is in the same eq class as the
	// cached result's output digest, and if not, merge them since we know them now to be equivalent
	res := hitTerm.result
	res.refCount++

	// NOTE: we skip id.ExtraDigests() here because we assume that an incoming ID with multiple digests
	// already has all those digests merged in the e-graph from previous operations.
	requestEqID := c.ensureEqClassForDigestLocked(id.Digest().String())
	c.mergeOutputsForTermDigestLocked(termDigest, requestEqID)

	// Materialize caller-facing result preserving request recipe identity.
	retID := id
	for _, extra := range hitTerm.outputExtraDigests {
		if extra.Digest == "" {
			continue
		}
		retID = retID.With(call.WithExtraDigest(extra))
	}
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

func (c *cache) indexResultForTermLocked(
	selfDigest digest.Digest,
	inputDigests []digest.Digest,
	outputEqID eqClassID,
	outputExtraDigests []call.ExtraDigest,
	res *sharedResult,
) {
	if outputEqID == 0 || res == nil {
		return
	}

	inputEqIDs := c.ensureTermInputEqIDsLocked(inputDigests)
	termDigest := calcEgraphTermDigest(selfDigest, inputEqIDs)

	if term := c.resultTermByDigestLocked(res, termDigest); term != nil {
		term.outputExtraDigests = mergeExtraDigestFacts(term.outputExtraDigests, outputExtraDigests)
		c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)
		return
	}

	outputEqID = c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)

	c.initEgraphLocked()
	termID := c.nextEgraphTermID
	c.nextEgraphTermID++

	term := newEgraphTerm(termID, selfDigest, inputEqIDs, outputEqID, outputExtraDigests, res)
	c.egraphTerms[termID] = term

	digestSet := c.egraphTermsByDigest[term.termDigest]
	if digestSet == nil {
		digestSet = make(map[egraphTermID]struct{})
		c.egraphTermsByDigest[term.termDigest] = digestSet
	}
	digestSet[termID] = struct{}{}

	for _, in := range term.inputEqIDs {
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

	resultSet := c.egraphResultTerms[res]
	if resultSet == nil {
		resultSet = make(map[egraphTermID]struct{})
		c.egraphResultTerms[res] = resultSet
	}
	resultSet[termID] = struct{}{}
}

func (c *cache) indexResultForIDLocked(
	id *call.ID,
	outputEqID eqClassID,
	outputExtraDigests []call.ExtraDigest,
	res *sharedResult,
) {
	if id == nil || outputEqID == 0 || res == nil {
		return
	}

	selfDigest, inputDigests, err := id.SelfDigestAndInputs()
	if err != nil {
		slog.Warn("failed to derive e-graph term digests", "err", err)
		return
	}
	c.indexResultForTermLocked(selfDigest, inputDigests, outputEqID, outputExtraDigests, res)
}

func (c *cache) outputEqClassForResultLocked(requestID *call.ID, res *sharedResult) eqClassID {
	digestSet := make(map[string]struct{}, 6)
	add := func(dig string) {
		if dig == "" {
			return
		}
		digestSet[dig] = struct{}{}
	}

	if requestID != nil {
		add(requestID.Digest().String())
		for _, extra := range requestID.ExtraDigests() {
			add(extra.Digest.String())
		}
	}
	if res != nil {
		add(res.outputDigest.String())
		for _, extra := range res.outputExtraDigests {
			add(extra.Digest.String())
		}
	}

	if len(digestSet) == 0 {
		return 0
	}

	ids := make([]eqClassID, 0, len(digestSet))
	for dig := range digestSet {
		ids = append(ids, c.ensureEqClassForDigestLocked(dig))
	}
	return c.mergeEqClassesLocked(ids...)
}

func (c *cache) indexResultInEgraphLocked(requestID *call.ID, res *sharedResult) {
	if requestID == nil || res == nil {
		return
	}
	outputEqID := c.outputEqClassForResultLocked(requestID, res)
	if outputEqID == 0 {
		return
	}
	c.indexResultForIDLocked(requestID, outputEqID, res.outputExtraDigests, res)
	if res.hasResultTerm {
		c.indexResultForTermLocked(
			res.resultTermSelf,
			res.resultTermInputs,
			outputEqID,
			res.outputExtraDigests,
			res,
		)
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
	c.egraphResultTerms = nil
	c.nextEgraphClassID = 0
	c.nextEgraphTermID = 0
}
