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

type egraphInputProvenanceKind string

const (
	egraphInputProvenanceKindResult egraphInputProvenanceKind = "result"
	egraphInputProvenanceKindDigest egraphInputProvenanceKind = "digest"
)

// egraphResultTermAssoc stores metadata for one result<->term association.
// Today this is just per-input provenance indicating whether each input slot
// was result-backed or digest-only when the association was created.
//
// NOTE: we intentionally assume for now that a congruent term will not be
// observed with conflicting provenance for the same input slot. If that ever
// does happen, the most recently observed provenance simply replaces the older
// one.
type egraphResultTermAssoc struct {
	inputProvenance []egraphInputProvenanceKind
}

func sameInputProvenance(a, b []egraphInputProvenanceKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type eqMergePair struct {
	a eqClassID
	b eqClassID
}

type persistedClosureGraph struct {
	resultIDs      map[sharedResultID]struct{}
	termIDs        map[egraphTermID]struct{}
	depIDsByParent map[sharedResultID]map[sharedResultID]struct{}
}

func (graph *persistedClosureGraph) addResult(resultID sharedResultID) {
	if resultID == 0 {
		return
	}
	if graph.resultIDs == nil {
		graph.resultIDs = make(map[sharedResultID]struct{})
	}
	graph.resultIDs[resultID] = struct{}{}
}

func (graph *persistedClosureGraph) addTerm(termID egraphTermID) {
	if termID == 0 {
		return
	}
	if graph.termIDs == nil {
		graph.termIDs = make(map[egraphTermID]struct{})
	}
	graph.termIDs[termID] = struct{}{}
}

func (graph *persistedClosureGraph) addDep(parentID, depID sharedResultID) {
	if parentID == 0 || depID == 0 || parentID == depID {
		return
	}
	if graph.depIDsByParent == nil {
		graph.depIDsByParent = make(map[sharedResultID]map[sharedResultID]struct{})
	}
	deps := graph.depIDsByParent[parentID]
	if deps == nil {
		deps = make(map[sharedResultID]struct{})
		graph.depIDsByParent[parentID] = deps
	}
	deps[depID] = struct{}{}
}

func touchSharedResultLastUsed(res *sharedResult, nowUnixNano int64) {
	if res == nil || nowUnixNano <= res.lastUsedAtUnixNano {
		return
	}
	res.lastUsedAtUnixNano = nowUnixNano
}

func calcEgraphTermDigest(selfDigest digest.Digest, inputEqIDs []eqClassID) string {
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputEqIDs {
		h = h.WithDelim().
			WithUint64(uint64(in))
	}
	return h.DigestAndClose()
}

func (c *cache) ensureTermInputEqIDsLocked(ctx context.Context, inputDigests []digest.Digest) []eqClassID {
	inputEqIDs := make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		inputEqIDs[i] = c.findEqClassLocked(c.ensureEqClassForDigestLocked(ctx, inDig.String()))
	}
	return inputEqIDs
}

func (c *cache) inputProvenanceForRefs(inputRefs []call.StructuralInputRef) ([]egraphInputProvenanceKind, error) {
	inputProvenance := make([]egraphInputProvenanceKind, 0, len(inputRefs))
	for _, ref := range inputRefs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		switch {
		case ref.ID != nil:
			inputProvenance = append(inputProvenance, egraphInputProvenanceKindResult)
		case ref.Digest != "":
			inputProvenance = append(inputProvenance, egraphInputProvenanceKindDigest)
		default:
			return nil, fmt.Errorf("structural input ref has neither ID nor digest")
		}
	}
	return inputProvenance, nil
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
	if c.eqClassToDigests == nil {
		c.eqClassToDigests = make(map[eqClassID]map[string]struct{})
	}
	if c.eqClassExtraDigests == nil {
		c.eqClassExtraDigests = make(map[eqClassID]map[call.ExtraDigest]struct{})
	}
	if c.inputEqClassToTerms == nil {
		c.inputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
	}
	if c.outputEqClassToTerms == nil {
		c.outputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
	}
	if c.resultOutputEqClasses == nil {
		c.resultOutputEqClasses = make(map[sharedResultID]map[eqClassID]struct{})
	}
	if c.egraphTerms == nil {
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
	}
	if c.egraphTermsByTermDigest == nil {
		c.egraphTermsByTermDigest = make(map[string]map[egraphTermID]struct{})
	}
	if c.egraphResultsByDigest == nil {
		c.egraphResultsByDigest = make(map[string]map[sharedResultID]struct{})
	}
	if c.termInputProvenance == nil {
		c.termInputProvenance = make(map[egraphTermID][]egraphInputProvenanceKind)
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

func (c *cache) ensureEqClassForDigestLocked(ctx context.Context, dig string) eqClassID {
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
	digests := c.eqClassToDigests[id]
	if digests == nil {
		digests = make(map[string]struct{}, 1)
		c.eqClassToDigests[id] = digests
	}
	digests[dig] = struct{}{}
	c.traceEqClassCreated(ctx, id, dig)
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

	// merge reverse digest indexes
	dstDigests := c.eqClassToDigests[ra]
	srcDigests := c.eqClassToDigests[rb]
	if len(srcDigests) > 0 {
		if dstDigests == nil {
			dstDigests = make(map[string]struct{}, len(srcDigests))
			c.eqClassToDigests[ra] = dstDigests
		}
		for dig := range srcDigests {
			dstDigests[dig] = struct{}{}
		}
		delete(c.eqClassToDigests, rb)
	}

	dstExtraDigests := c.eqClassExtraDigests[ra]
	srcExtraDigests := c.eqClassExtraDigests[rb]
	if len(srcExtraDigests) > 0 {
		if dstExtraDigests == nil {
			dstExtraDigests = make(map[call.ExtraDigest]struct{}, len(srcExtraDigests))
			c.eqClassExtraDigests[ra] = dstExtraDigests
		}
		for extra := range srcExtraDigests {
			dstExtraDigests[extra] = struct{}{}
		}
		delete(c.eqClassExtraDigests, rb)
	}

	// merge reverse input-term indexes
	dst := c.inputEqClassToTerms[ra]
	src := c.inputEqClassToTerms[rb]
	if len(src) > 0 {
		if dst == nil {
			dst = make(map[egraphTermID]struct{}, len(src))
			c.inputEqClassToTerms[ra] = dst
		}
		for termID := range src {
			dst[termID] = struct{}{}
		}
		delete(c.inputEqClassToTerms, rb)
	}

	// merge reverse output-term indexes
	dstOutputTerms := c.outputEqClassToTerms[ra]
	srcOutputTerms := c.outputEqClassToTerms[rb]
	if len(srcOutputTerms) > 0 {
		if dstOutputTerms == nil {
			dstOutputTerms = make(map[egraphTermID]struct{}, len(srcOutputTerms))
			c.outputEqClassToTerms[ra] = dstOutputTerms
		}
		for termID := range srcOutputTerms {
			dstOutputTerms[termID] = struct{}{}
		}
		delete(c.outputEqClassToTerms, rb)
	}

	return ra
}

func (c *cache) mergeEqClassesLocked(ctx context.Context, ids ...eqClassID) eqClassID {
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
	c.traceEqClassMerged(ctx, ids, root)

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
		for _, pair := range c.repairClassTermsLocked(ctx, cur) {
			next := c.mergeEqClassesNoRepairLocked(pair.a, pair.b)
			if next != 0 {
				toRepair = append(toRepair, next)
			}
		}
	}
	return c.findEqClassLocked(ids[0])
}

func (c *cache) repairClassTermsLocked(ctx context.Context, root eqClassID) (merges []eqMergePair) {
	termSet := c.inputEqClassToTerms[root]
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
			oldInputsCopy := append([]eqClassID(nil), oldInputs...)
			// Re-home this term under canonical input classes.
			for _, in := range oldInputs {
				if set := c.inputEqClassToTerms[in]; set != nil {
					delete(set, termID)
					if len(set) == 0 {
						delete(c.inputEqClassToTerms, in)
					}
				}
			}
			for _, in := range newInputs {
				if in == 0 {
					continue
				}
				set := c.inputEqClassToTerms[in]
				if set == nil {
					set = make(map[egraphTermID]struct{})
					c.inputEqClassToTerms[in] = set
				}
				set[termID] = struct{}{}
			}
			term.inputEqIDs = newInputs
			c.traceTermInputsRepaired(ctx, term.id, oldInputsCopy, newInputs)
			c.traceTermRehomedUnderEqClasses(ctx, term.id, newInputs)
		}

		newTermDigest := calcEgraphTermDigest(term.selfDigest, term.inputEqIDs)
		if newTermDigest != term.termDigest {
			oldTermDigest := term.termDigest
			if set := c.egraphTermsByTermDigest[term.termDigest]; set != nil {
				delete(set, termID)
				if len(set) == 0 {
					delete(c.egraphTermsByTermDigest, term.termDigest)
				}
			}
			set := c.egraphTermsByTermDigest[newTermDigest]
			if set == nil {
				set = make(map[egraphTermID]struct{})
				c.egraphTermsByTermDigest[newTermDigest] = set
			}
			set[termID] = struct{}{}
			term.termDigest = newTermDigest
			c.traceTermDigestRecomputed(ctx, term.id, oldTermDigest, newTermDigest)
		}

		set := c.egraphTermsByTermDigest[term.termDigest]
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
			c.traceTermOutputsMerged(ctx, first.id, other.id, first.outputEqID, other.outputEqID)
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

func (c *cache) firstResultForTermSetDeterministicallyAtLocked(
	termSet map[egraphTermID]struct{},
	nowUnix int64,
) *sharedResult {
	seenOutputEqClasses := make(map[eqClassID]struct{}, len(termSet))
	var bestID sharedResultID
	for termID := range termSet {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		outputEqID := c.findEqClassLocked(term.outputEqID)
		if outputEqID == 0 {
			continue
		}
		if _, ok := seenOutputEqClasses[outputEqID]; ok {
			continue
		}
		seenOutputEqClasses[outputEqID] = struct{}{}
		res := c.firstResultForOutputEqClassDeterministicallyAtLocked(outputEqID, nowUnix)
		if res == nil {
			continue
		}
		if bestID == 0 || res.id < bestID {
			bestID = res.id
		}
	}
	return c.resultsByID[bestID]
}

func (c *cache) firstResultForOutputEqClassDeterministicallyAtLocked(
	outputEqID eqClassID,
	nowUnix int64,
) *sharedResult {
	outputEqID = c.findEqClassLocked(outputEqID)
	if outputEqID == 0 {
		return nil
	}
	digests := c.eqClassToDigests[outputEqID]
	if len(digests) == 0 {
		return nil
	}
	var bestID sharedResultID
	for dig := range digests {
		res := c.firstResultDeterministicallyAtLocked(c.egraphResultsByDigest[dig], nowUnix)
		if res == nil {
			continue
		}
		if bestID == 0 || res.id < bestID {
			bestID = res.id
		}
	}
	return c.resultsByID[bestID]
}

func (c *cache) firstResultForOutputEqClassAnyAtLocked(outputEqID eqClassID) *sharedResult {
	outputEqID = c.findEqClassLocked(outputEqID)
	if outputEqID == 0 {
		return nil
	}
	digests := c.eqClassToDigests[outputEqID]
	if len(digests) == 0 {
		return nil
	}
	var bestID sharedResultID
	for dig := range digests {
		for resID := range c.egraphResultsByDigest[dig] {
			res := c.resultsByID[resID]
			if res == nil {
				continue
			}
			if bestID == 0 || res.id < bestID {
				bestID = res.id
			}
		}
	}
	return c.resultsByID[bestID]
}

func (c *cache) deterministicDigestForEqClassLocked(eqID eqClassID) digest.Digest {
	eqID = c.findEqClassLocked(eqID)
	if eqID == 0 {
		return ""
	}
	var best string
	for dig := range c.eqClassToDigests[eqID] {
		if best == "" || dig < best {
			best = dig
		}
	}
	return digest.Digest(best)
}

func (c *cache) resultExpiredAtLocked(res *sharedResult, nowUnix int64) bool {
	if res == nil || res.expiresAtUnix == 0 {
		return false
	}
	return nowUnix >= res.expiresAtUnix
}

func (c *cache) firstTermForResultLocked(resID sharedResultID) *egraphTerm {
	for termID := range c.termIDsForResultLocked(resID) {
		term := c.egraphTerms[termID]
		if term != nil {
			return term
		}
	}
	return nil
}

type lookupMatch struct {
	selfDigest            digest.Digest
	inputDigests          []digest.Digest
	inputEqIDs            []eqClassID
	primaryLookupPossible bool
	missingInputIndex     int
	hitEqClassID          eqClassID
	hitTerm               *egraphTerm
	hitRes                *sharedResult
	termDigest            string
	termSetSize           int
}

func (c *cache) lookupMatchForIDLocked(ctx context.Context, id *call.ID) (lookupMatch, error) {
	match := lookupMatch{
		primaryLookupPossible: true,
		missingInputIndex:     -1,
	}
	if id == nil {
		return match, nil
	}

	nowUnix := time.Now().Unix()

	// first do a fast path check by id digests (recipe and extra)

	resultSet := c.egraphResultsByDigest[id.Digest().String()]
	match.hitRes = c.firstResultDeterministicallyAtLocked(resultSet, nowUnix)
	if match.hitRes != nil {
		match.hitTerm = c.firstTermForResultLocked(match.hitRes.id)
		match.hitEqClassID = c.findEqClassLocked(c.egraphDigestToClass[id.Digest().String()])
		return match, nil
	}
	for _, dig := range id.ExtraDigests() {
		if dig.Digest == "" {
			continue
		}
		resultSet := c.egraphResultsByDigest[dig.Digest.String()]
		match.hitRes = c.firstResultDeterministicallyAtLocked(resultSet, nowUnix)
		if match.hitRes == nil {
			continue
		}
		match.hitTerm = c.firstTermForResultLocked(match.hitRes.id)
		match.hitEqClassID = c.findEqClassLocked(c.egraphDigestToClass[dig.Digest.String()])
		return match, nil
	}

	// do a full structural lookup based on the abstracted term (self digest + input eq classes)

	selfDigest, inputDigests, err := id.SelfDigestAndInputs()
	if err != nil {
		return match, fmt.Errorf("derive call term: %w", err)
	}
	match.selfDigest = selfDigest
	match.inputDigests = inputDigests
	match.inputEqIDs = make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		classID, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			match.primaryLookupPossible = false
			match.missingInputIndex = i
			break
		}
		root := c.findEqClassLocked(classID)
		if root == 0 {
			match.primaryLookupPossible = false
			match.missingInputIndex = i
			break
		}
		match.inputEqIDs[i] = root
	}
	if match.primaryLookupPossible {
		match.termDigest = calcEgraphTermDigest(selfDigest, match.inputEqIDs)
		termSet := c.egraphTermsByTermDigest[match.termDigest]
		match.termSetSize = len(termSet)
		match.hitTerm = c.firstLiveTermInSetLocked(termSet)
		match.hitRes = c.firstResultForTermSetDeterministicallyAtLocked(termSet, nowUnix)
		if match.hitTerm != nil {
			match.hitEqClassID = c.findEqClassLocked(match.hitTerm.outputEqID)
		}
	}
	return match, nil
}

func (c *cache) resolveSharedResultForInputIDLocked(ctx context.Context, id *call.ID) (*sharedResult, error) {
	match, err := c.lookupMatchForIDLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if match.hitRes == nil {
		return nil, fmt.Errorf("no cached shared result found for structural input %s", id.Digest())
	}
	return match.hitRes, nil
}

func (c *cache) indexResultDigestsLocked(res *sharedResult, requestID, responseID *call.ID) {
	if res == nil {
		return
	}
	c.initEgraphLocked()

	indexDigest := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		set := c.egraphResultsByDigest[dig.String()]
		if set == nil {
			set = make(map[sharedResultID]struct{})
			c.egraphResultsByDigest[dig.String()] = set
		}
		set[res.id] = struct{}{}
	}

	if requestID != nil {
		indexDigest(requestID.Digest())
		for _, extra := range requestID.ExtraDigests() {
			indexDigest(extra.Digest)
		}
	}
	if responseID != nil {
		indexDigest(responseID.Digest())
		for _, extra := range responseID.ExtraDigests() {
			indexDigest(extra.Digest)
		}
	}
}

func (c *cache) removeResultDigestsLocked(resID sharedResultID, outputEqClasses map[eqClassID]struct{}) {
	if resID == 0 || len(outputEqClasses) == 0 {
		return
	}

	for outputEqID := range outputEqClasses {
		outputEqID = c.findEqClassLocked(outputEqID)
		if outputEqID == 0 {
			continue
		}
		for dig := range c.eqClassToDigests[outputEqID] {
			set := c.egraphResultsByDigest[dig]
			if set == nil {
				continue
			}
			delete(set, resID)
			if len(set) == 0 {
				delete(c.egraphResultsByDigest, dig)
			}
		}
	}
}

// lookupCacheForID checks if the given call ID has an equivalent result in the cache. It first
// attempts direct digest lookup using the request recipe/extra digests. If that misses, it falls
// back to the canonical term lookup using (self, input eq-classes).
//
// This method assumes egraphMu is already held by the caller.
func (c *cache) lookupCacheForID(
	ctx context.Context,
	id *call.ID,
	persistable bool,
	ttlSeconds int64,
) (AnyResult, bool, error) {
	// (self digest, input eqSet IDs) are digested to create the "real" cache key we do a lookup on.
	// Figure those out first.
	if id == nil {
		return nil, false, nil
	}
	match, err := c.lookupMatchForIDLocked(ctx, id)
	if err != nil {
		return nil, false, err
	}
	c.traceLookupAttempt(ctx, id.Digest().String(), match.selfDigest.String(), match.inputDigests, persistable)
	hitTerm := match.hitTerm
	hitRes := match.hitRes

	if hitRes == nil {
		c.traceLookupMissNoMatch(ctx, id.Digest().String(), match.primaryLookupPossible, match.missingInputIndex, match.termDigest, match.termSetSize)
		return nil, false, nil
	}

	if !hitRes.hasValue && hitRes.persistedEnvelope != nil {
		// Imported envelopes are decoded lazily on hit. If this context cannot
		// decode the envelope shape (for example object IDs requiring server.Load,
		// or custom scalar/list types when no server is present), treat as miss so
		// the resolver path can re-materialize safely instead of failing or
		// recursing.
		if !persistedEnvelopeDecodableInContext(ctx, hitRes.persistedEnvelope) {
			c.traceLookupMissUndecodableEnvelope(ctx, id.Digest().String(), hitRes.id)
			return nil, false, nil
		}
	}

	// We have a cache hit. Teach this request identity onto the existing shared
	// result so any raw ID we hand back is itself resolvable by the cache later.
	res := hitRes
	// A TTL-bearing call can alias an existing result on lookup; apply the same
	// conservative expiry merge policy here so TTL remains effective on hits.
	now := time.Now()
	nowUnix := now.Unix()
	res.expiresAtUnix = mergeSharedResultExpiryUnix(
		res.expiresAtUnix,
		candidateSharedResultExpiryUnix(nowUnix, ttlSeconds),
	)
	touchSharedResultLastUsed(res, now.UnixNano())
	if persistable {
		// NOTE: this is an intentional experiment behavior. If a persistable field
		// hits a result originally produced by a non-persistable field, we
		// "upgrade" the shared result to persisted-dependency liveness so future
		// releases do not drop it or its dependency chain. This avoids surprising
		// misses for persistable callsites, but should be revisited when real
		// persistence policy is finalized.
		c.markResultAsDepOfPersistedLocked(ctx, res)
	}
	if err := c.teachResultIdentityLocked(ctx, res, id); err != nil {
		return nil, false, err
	}
	newRefCount := atomic.AddInt64(&res.refCount, 1)
	c.traceRefAcquired(ctx, res, newRefCount)

	// Materialize caller-facing result preserving request recipe identity.
	retID := id
	for outputEqID := range c.outputEqClassesForResultLocked(res.id) {
		// NOTE: if multiple content-labeled digests end up in one eq class, we
		// intentionally tolerate that for now and just use the first one we
		// encounter.
		for extra := range c.eqClassExtraDigests[outputEqID] {
			if extra.Label != call.ExtraDigestLabelContent || extra.Digest == "" {
				continue
			}
			retID = retID.With(call.WithExtraDigest(extra))
			break
		}
		if retID.ContentDigest() != "" {
			break
		}
	}
	retID = retID.AppendEffectIDs(res.outputEffectIDs...)
	if !res.hasValue {
		c.traceLookupHit(ctx, id.Digest().String(), res, hitTerm, match.termDigest)
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
		c.traceLookupHit(ctx, id.Digest().String(), res, hitTerm, match.termDigest)
		return retRes, true, nil
	}
	retObjRes, err := res.objType.New(retRes)
	if err != nil {
		return nil, false, fmt.Errorf("reconstruct structural-hit object result from cache: %w", err)
	}
	c.traceLookupHit(ctx, id.Digest().String(), res, hitTerm, match.termDigest)
	return retObjRes, true, nil
}

func persistedEnvelopeDecodableInContext(ctx context.Context, env *PersistedResultEnvelope) bool {
	if env == nil {
		return true
	}
	srv := CurrentDagqlServer(ctx)
	switch env.Kind {
	case persistedResultKindNull:
		return true
	case persistedResultKindObject:
		if srv == nil {
			return false
		}
		objType, ok := srv.ObjectType(env.TypeName)
		if !ok {
			return false
		}
		_, ok = objType.Typed().(PersistedObjectDecoder)
		return ok
	case persistedResultKindScalar:
		if isBuiltinPersistedScalarType(env.TypeName) {
			return true
		}
		return srv != nil
	case persistedResultKindList:
		if srv != nil {
			return true
		}
		for i := range env.Items {
			if !persistedEnvelopeDecodableInContext(ctx, &env.Items[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isBuiltinPersistedScalarType(typeName string) bool {
	switch typeName {
	case "String", "Int", "Float", "Boolean", "JSON", "Void":
		return true
	default:
		return false
	}
}

func (c *cache) termForResultByDigestLocked(resID sharedResultID, termDigest string) *egraphTerm {
	for termID := range c.termIDsForResultLocked(resID) {
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

func (c *cache) outputEqClassesForResultLocked(resID sharedResultID) map[eqClassID]struct{} {
	classes := c.resultOutputEqClasses[resID]
	if len(classes) == 0 {
		return nil
	}
	out := make(map[eqClassID]struct{}, len(classes))
	for eqID := range classes {
		root := c.findEqClassLocked(eqID)
		if root == 0 {
			continue
		}
		out[root] = struct{}{}
	}
	return out
}

func (c *cache) termIDsForResultLocked(resID sharedResultID) map[egraphTermID]struct{} {
	outputEqClasses := c.outputEqClassesForResultLocked(resID)
	if len(outputEqClasses) == 0 {
		return nil
	}
	termIDs := make(map[egraphTermID]struct{})
	for outputEqID := range outputEqClasses {
		for termID := range c.outputEqClassToTerms[outputEqID] {
			termIDs[termID] = struct{}{}
		}
	}
	return termIDs
}

func (c *cache) mergeOutputsForTermDigestLocked(ctx context.Context, termDigest string, outputEqID eqClassID) eqClassID {
	set := c.egraphTermsByTermDigest[termDigest]
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
	root := c.mergeEqClassesLocked(ctx, mergeIDs...)
	for termID := range set {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		term.outputEqID = root
	}
	return root
}

func (c *cache) associateResultWithTermLocked(
	ctx context.Context,
	res *sharedResult,
	termID egraphTermID,
	inputProvenance []egraphInputProvenanceKind,
) {
	if res == nil || res.id == 0 || termID == 0 {
		return
	}
	term := c.egraphTerms[termID]
	if term == nil {
		return
	}
	outputEqID := c.findEqClassLocked(term.outputEqID)
	if outputEqID == 0 {
		return
	}
	if existing, ok := c.termInputProvenance[termID]; ok {
		if !sameInputProvenance(existing, inputProvenance) {
			c.termInputProvenance[termID] = slices.Clone(inputProvenance)
			c.traceResultTermAssocUpdated(ctx, res.id, termID, inputProvenance)
		}
	} else {
		c.termInputProvenance[termID] = slices.Clone(inputProvenance)
	}

	resultOutputEqClasses := c.resultOutputEqClasses[res.id]
	if resultOutputEqClasses == nil {
		resultOutputEqClasses = make(map[eqClassID]struct{})
		c.resultOutputEqClasses[res.id] = resultOutputEqClasses
	}
	if _, ok := resultOutputEqClasses[outputEqID]; !ok {
		resultOutputEqClasses[outputEqID] = struct{}{}
		c.traceResultTermAssocAdded(ctx, res.id, termID, inputProvenance)
	}
}

func (c *cache) teachResultIdentityLocked(ctx context.Context, res *sharedResult, requestID *call.ID) error {
	if res == nil || res.id == 0 || requestID == nil {
		return nil
	}
	c.initEgraphLocked()

	requestSelf, requestInputRefs, err := requestID.SelfDigestAndInputRefs()
	if err != nil {
		return fmt.Errorf("derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.InputDigest()
		if err != nil {
			return fmt.Errorf("derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	rootSet := c.outputEqClassesForResultLocked(res.id)
	if rootSet == nil {
		rootSet = make(map[eqClassID]struct{})
	}
	if requestID.Digest() != "" {
		if eqID := c.ensureEqClassForDigestLocked(ctx, requestID.Digest().String()); eqID != 0 {
			rootSet[c.findEqClassLocked(eqID)] = struct{}{}
		}
	}
	for _, extra := range requestID.ExtraDigests() {
		if extra.Digest == "" {
			continue
		}
		if eqID := c.ensureEqClassForDigestLocked(ctx, extra.Digest.String()); eqID != 0 {
			rootSet[c.findEqClassLocked(eqID)] = struct{}{}
		}
	}
	if len(rootSet) == 0 {
		return nil
	}

	mergeIDs := make([]eqClassID, 0, len(rootSet))
	for root := range rootSet {
		if root == 0 {
			continue
		}
		mergeIDs = append(mergeIDs, root)
	}
	if len(mergeIDs) == 0 {
		return nil
	}
	outputEqID := c.mergeEqClassesLocked(ctx, mergeIDs...)
	if outputEqID == 0 {
		return nil
	}

	inputProvenance, err := c.inputProvenanceForRefs(requestInputRefs)
	if err != nil {
		return fmt.Errorf("derive input provenance for request term %s: %w", requestSelf, err)
	}
	inputEqIDs := c.ensureTermInputEqIDsLocked(ctx, requestInputs)
	termDigest := calcEgraphTermDigest(requestSelf, inputEqIDs)

	switch {
	case c.termForResultByDigestLocked(res.id, termDigest) != nil:
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
	case c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest[termDigest]) != nil:
		existingTerm := c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest[termDigest])
		c.associateResultWithTermLocked(ctx, res, existingTerm.id, inputProvenance)
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
	default:
		mergedOutputEqID := c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)

		termID := c.nextEgraphTermID
		c.nextEgraphTermID++

		newTerm := newEgraphTerm(termID, requestSelf, inputEqIDs, mergedOutputEqID)
		c.egraphTerms[termID] = newTerm
		c.traceTermCreated(ctx, "runtime", "", newTerm)

		digestTerms := c.egraphTermsByTermDigest[newTerm.termDigest]
		if digestTerms == nil {
			digestTerms = make(map[egraphTermID]struct{})
			c.egraphTermsByTermDigest[newTerm.termDigest] = digestTerms
		}
		digestTerms[termID] = struct{}{}

		for _, inEqID := range newTerm.inputEqIDs {
			if inEqID == 0 {
				continue
			}
			classTerms := c.inputEqClassToTerms[inEqID]
			if classTerms == nil {
				classTerms = make(map[egraphTermID]struct{})
				c.inputEqClassToTerms[inEqID] = classTerms
			}
			classTerms[termID] = struct{}{}
		}
		outputTerms := c.outputEqClassToTerms[mergedOutputEqID]
		if outputTerms == nil {
			outputTerms = make(map[egraphTermID]struct{})
			c.outputEqClassToTerms[mergedOutputEqID] = outputTerms
		}
		outputTerms[termID] = struct{}{}

		c.associateResultWithTermLocked(ctx, res, termID, inputProvenance)
	}

	c.indexResultDigestsLocked(res, requestID, nil)
	for termID := range c.termIDsForResultLocked(res.id) {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		outputEqID := c.findEqClassLocked(term.outputEqID)
		if outputEqID == 0 {
			continue
		}
		extras := c.eqClassExtraDigests[outputEqID]
		if extras == nil {
			extras = make(map[call.ExtraDigest]struct{})
			c.eqClassExtraDigests[outputEqID] = extras
		}
		for _, extra := range requestID.ExtraDigests() {
			if extra.Digest == "" {
				continue
			}
			extras[extra] = struct{}{}
		}
	}

	return nil
}

func (c *cache) indexWaitResultInEgraphLocked(
	ctx context.Context,
	requestID *call.ID,
	responseID *call.ID,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []call.StructuralInputRef,
	resultTermSelf digest.Digest,
	resultTermInputs []digest.Digest,
	resultTermInputRefs []call.StructuralInputRef,
	hasResultTerm bool,
	res *sharedResult,
) error {
	c.initEgraphLocked()

	digestSet := make(map[string]struct{}, 6)
	addDigest := func(dig string) {
		if dig == "" {
			return
		}
		digestSet[dig] = struct{}{}
	}

	if requestID != nil {
		addDigest(requestID.Digest().String())
		for _, extra := range requestID.ExtraDigests() {
			addDigest(extra.Digest.String())
		}
	}
	if responseID != nil {
		addDigest(responseID.Digest().String())
		for _, extra := range responseID.ExtraDigests() {
			addDigest(extra.Digest.String())
		}
	}
	if len(digestSet) == 0 {
		return nil
	}

	// each digest associated with the request and result are all eq classes that will now
	// be merged. Gather them and their current eq classes.
	rootSet := make(map[eqClassID]struct{}, len(digestSet))
	for dig := range digestSet {
		if id := c.ensureEqClassForDigestLocked(ctx, dig); id != 0 {
			rootSet[id] = struct{}{}
		}
	}
	if len(rootSet) == 0 {
		return nil
	}

	// merge all the eq classes for the digests into the same eq class
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
		outputEqID = c.mergeEqClassesLocked(ctx, mergeIDs...)
	}
	if outputEqID == 0 {
		return nil
	}

	// ensure the result has its int ID initialized, then index it by that int it
	// and by all of its digests in our various maps
	if res.id == 0 {
		res.id = c.nextSharedResultID
		c.nextSharedResultID++
	}
	c.resultsByID[res.id] = res
	if err := c.ensureResultCallFrameLocked(ctx, res, requestID); err != nil {
		return fmt.Errorf("derive result call frame: %w", err)
	}
	setTypedPersistedResultID(res.self, res.id)
	c.traceResultCreated(ctx, res)

	//
	// associate the result with the relevant terms and apply any necessary eq class unions + repairs
	//

	termsToIndex := []struct {
		selfDigest   digest.Digest
		inputDigests []digest.Digest
		inputRefs    []call.StructuralInputRef
	}{
		{
			selfDigest:   requestSelf,
			inputDigests: requestInputs,
			inputRefs:    requestInputRefs,
		},
	}
	// Only index both input+return terms if they are actually different
	shouldIndexResultTerm := hasResultTerm
	if shouldIndexResultTerm && requestSelf == resultTermSelf && slices.Equal(requestInputs, resultTermInputs) {
		shouldIndexResultTerm = false
	}
	if shouldIndexResultTerm {
		termsToIndex = append(termsToIndex, struct {
			selfDigest   digest.Digest
			inputDigests []digest.Digest
			inputRefs    []call.StructuralInputRef
		}{
			selfDigest:   resultTermSelf,
			inputDigests: resultTermInputs,
			inputRefs:    resultTermInputRefs,
		})
	}

	for _, term := range termsToIndex {
		inputProvenance, err := c.inputProvenanceForRefs(term.inputRefs)
		if err != nil {
			return fmt.Errorf("derive input provenance for term %s: %w", term.selfDigest, err)
		}
		// get all the eq classes for the inputs
		inputEqIDs := c.ensureTermInputEqIDsLocked(ctx, term.inputDigests)
		// calculate the term digest based on the resolved input eq classes
		termDigest := calcEgraphTermDigest(term.selfDigest, inputEqIDs)

		if existingTerm := c.termForResultByDigestLocked(res.id, termDigest); existingTerm != nil {
			// we already setup this res with this term, just ensure that the output eq class
			// (which is the merged eq class containing all input req digests + return val digests)
			// is associated as the output eq class for this term; doing a merge+replair if needed.
			c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
			continue
		}
		if existingTerm := c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest[termDigest]); existingTerm != nil {
			// we ended up with a duplicate term that has the same digest; just associate this
			// result with that term and merge the output eq class as needed, no need to create
			// a new term
			c.associateResultWithTermLocked(ctx, res, existingTerm.id, inputProvenance)
			c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
			continue
		}

		// no existing term with this digest, create a new one and associate it with this result; also merge the output eq class as needed

		mergedOutputEqID := c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)

		termID := c.nextEgraphTermID
		c.nextEgraphTermID++

		newTerm := newEgraphTerm(
			termID,
			term.selfDigest,
			inputEqIDs,
			mergedOutputEqID,
		)
		c.egraphTerms[termID] = newTerm
		c.traceTermCreated(ctx, "runtime", "", newTerm)

		digestTerms := c.egraphTermsByTermDigest[newTerm.termDigest]
		if digestTerms == nil {
			digestTerms = make(map[egraphTermID]struct{})
			c.egraphTermsByTermDigest[newTerm.termDigest] = digestTerms
		}
		digestTerms[termID] = struct{}{}

		for _, inEqID := range newTerm.inputEqIDs {
			if inEqID == 0 {
				continue
			}
			classTerms := c.inputEqClassToTerms[inEqID]
			if classTerms == nil {
				classTerms = make(map[egraphTermID]struct{})
				c.inputEqClassToTerms[inEqID] = classTerms
			}
			classTerms[termID] = struct{}{}
		}
		outputTerms := c.outputEqClassToTerms[mergedOutputEqID]
		if outputTerms == nil {
			outputTerms = make(map[egraphTermID]struct{})
			c.outputEqClassToTerms[mergedOutputEqID] = outputTerms
		}
		outputTerms[termID] = struct{}{}

		c.associateResultWithTermLocked(ctx, res, termID, inputProvenance)
	}

	c.indexResultDigestsLocked(res, requestID, responseID)
	for termID := range c.termIDsForResultLocked(res.id) {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		outputEqID := c.findEqClassLocked(term.outputEqID)
		if outputEqID == 0 {
			continue
		}
		extras := c.eqClassExtraDigests[outputEqID]
		if extras == nil {
			extras = make(map[call.ExtraDigest]struct{})
			c.eqClassExtraDigests[outputEqID] = extras
		}
		if requestID != nil {
			for _, extra := range requestID.ExtraDigests() {
				if extra.Digest == "" {
					continue
				}
				extras[extra] = struct{}{}
			}
		}
		if responseID != nil {
			for _, extra := range responseID.ExtraDigests() {
				if extra.Digest == "" {
					continue
				}
				extras[extra] = struct{}{}
			}
		}
	}

	// TODO: ??? Why do we do this here...?
	if res.depOfPersistedResult {
		c.markResultAsDepOfPersistedLocked(ctx, res)
	}
	return nil
}

func addPersistedFrameRefDep(
	parentID sharedResultID,
	ref *ResultCallFrameRef,
	graph *persistedClosureGraph,
	stack *[]sharedResultID,
) {
	if ref == nil {
		return
	}
	depID := sharedResultID(ref.ResultID)
	if depID == 0 || depID == parentID {
		return
	}
	graph.addDep(parentID, depID)
	*stack = append(*stack, depID)
}

func addPersistedFrameLiteralDeps(
	parentID sharedResultID,
	lit *ResultCallFrameLiteral,
	graph *persistedClosureGraph,
	stack *[]sharedResultID,
) {
	if lit == nil {
		return
	}
	switch lit.Kind {
	case ResultCallFrameLiteralKindResultRef:
		addPersistedFrameRefDep(parentID, lit.ResultRef, graph, stack)
	case ResultCallFrameLiteralKindList:
		for _, item := range lit.ListItems {
			addPersistedFrameLiteralDeps(parentID, item, graph, stack)
		}
	case ResultCallFrameLiteralKindObject:
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			addPersistedFrameLiteralDeps(parentID, field.Value, graph, stack)
		}
	}
}

func addPersistedFrameDeps(
	parentID sharedResultID,
	frame *ResultCallFrame,
	graph *persistedClosureGraph,
	stack *[]sharedResultID,
) {
	if frame == nil {
		return
	}
	addPersistedFrameRefDep(parentID, frame.Receiver, graph, stack)
	if frame.Module != nil {
		addPersistedFrameRefDep(parentID, frame.Module.ResultRef, graph, stack)
	}
	for _, arg := range frame.Args {
		if arg == nil {
			continue
		}
		addPersistedFrameLiteralDeps(parentID, arg.Value, graph, stack)
	}
	for _, input := range frame.ImplicitInputs {
		if input == nil {
			continue
		}
		addPersistedFrameLiteralDeps(parentID, input.Value, graph, stack)
	}
}

// walks exact materialized dependencies for persisted results: explicit
// sharedResult.deps plus exact result refs reachable through resultCallFrame
// metadata. Symbolic term/eq-class state is still gathered for persisted cache
// hitability, but no longer pulls in extra materialized results by provenance.
func (c *cache) persistedClosureGraphLocked(rootResultID sharedResultID) persistedClosureGraph {
	if rootResultID == 0 {
		return persistedClosureGraph{}
	}
	graph := persistedClosureGraph{
		resultIDs:      make(map[sharedResultID]struct{}),
		termIDs:        make(map[egraphTermID]struct{}),
		depIDsByParent: make(map[sharedResultID]map[sharedResultID]struct{}),
	}
	stack := []sharedResultID{rootResultID}
	seen := make(map[sharedResultID]struct{})
	for len(stack) > 0 {
		n := len(stack) - 1
		curID := stack[n]
		stack = stack[:n]
		if _, ok := seen[curID]; ok {
			continue
		}
		seen[curID] = struct{}{}
		res := c.resultsByID[curID]
		if res == nil {
			continue
		}
		graph.addResult(curID)
		for depID := range res.deps {
			if depID == 0 || depID == curID {
				continue
			}
			graph.addDep(curID, depID)
			stack = append(stack, depID)
		}
		addPersistedFrameDeps(curID, res.resultCallFrame, &graph, &stack)
		for termID := range c.termIDsForResultLocked(curID) {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			graph.addTerm(termID)
		}
	}
	return graph
}

func (c *cache) markResultAsDepOfPersistedLocked(ctx context.Context, root *sharedResult) bool {
	if root == nil {
		return false
	}
	changed := false
	// Persisted-closure invariant: once a result is marked as dependency-of-
	// persisted, every transitive materialized dependency reachable through
	// explicit deps and resultCallFrame refs must also be marked.
	graph := c.persistedClosureGraphLocked(root.id)
	for curID := range graph.resultIDs {
		cur := c.resultsByID[curID]
		if cur == nil {
			continue
		}
		if !cur.depOfPersistedResult {
			changed = true
			c.tracePersistRootMarked(ctx, cur.id, root.id, "runtime", "")
		}
		cur.depOfPersistedResult = true
	}
	return changed
}

func (c *cache) removeResultFromEgraphLocked(ctx context.Context, res *sharedResult) error {
	if res == nil {
		return nil
	}
	if len(c.egraphTerms) == 0 || len(c.resultOutputEqClasses) == 0 {
		c.maybeResetEgraphLocked()
		return nil
	}

	affectedOutputEqClasses := c.outputEqClassesForResultLocked(res.id)
	for termID := range c.termIDsForResultLocked(res.id) {
		c.traceResultTermAssocRemoved(ctx, res.id, termID)
	}
	c.removeResultDigestsLocked(res.id, affectedOutputEqClasses)
	delete(c.resultOutputEqClasses, res.id)
	delete(c.resultsByID, res.id)
	c.traceResultRemoved(ctx, res)

	nowUnix := time.Now().Unix()
	for outputEqID := range affectedOutputEqClasses {
		if c.firstResultForOutputEqClassDeterministicallyAtLocked(outputEqID, nowUnix) != nil {
			// still some results in this eq class, nothing to clean up yet
			continue
		}

		// no more results in this eq class, get the list of terms with this output eq class and clean them up
		// since their results were pruned
		termIDs := make([]egraphTermID, 0, len(c.outputEqClassToTerms[outputEqID]))
		for termID := range c.outputEqClassToTerms[outputEqID] {
			termIDs = append(termIDs, termID)
		}
		for _, termID := range termIDs {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			if set := c.egraphTermsByTermDigest[term.termDigest]; set != nil {
				delete(set, termID)
				if len(set) == 0 {
					delete(c.egraphTermsByTermDigest, term.termDigest)
				}
			}
			for _, in := range term.inputEqIDs {
				if set := c.inputEqClassToTerms[in]; set != nil {
					delete(set, termID)
					if len(set) == 0 {
						delete(c.inputEqClassToTerms, in)
					}
				}
			}
			if set := c.outputEqClassToTerms[outputEqID]; set != nil {
				delete(set, termID)
			}
			delete(c.egraphTerms, termID)
			delete(c.termInputProvenance, termID)
			c.traceTermRemoved(ctx, termID)
		}
		delete(c.outputEqClassToTerms, outputEqID)
	}
	c.maybeResetEgraphLocked()
	return nil
}

func (c *cache) maybeResetEgraphLocked() {
	if len(c.egraphTerms) != 0 {
		return
	}

	c.egraphDigestToClass = nil
	c.egraphParents = nil
	c.egraphRanks = nil
	c.eqClassToDigests = nil
	c.eqClassExtraDigests = nil
	c.inputEqClassToTerms = nil
	c.outputEqClassToTerms = nil
	c.resultOutputEqClasses = nil
	c.egraphTerms = nil
	c.termInputProvenance = nil
	c.egraphTermsByTermDigest = nil
	c.egraphResultsByDigest = nil
	c.resultsByID = nil
	c.nextEgraphClassID = 0
	c.nextEgraphTermID = 0
	c.nextSharedResultID = 0
}
