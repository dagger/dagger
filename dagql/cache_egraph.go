package dagql

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
	set "github.com/hashicorp/go-set/v3"
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

func touchSharedResultLastUsed(res *sharedResult, nowUnixNano int64) {
	if res == nil {
		return
	}
	res.payloadMu.Lock()
	if nowUnixNano > res.lastUsedAtUnixNano {
		res.lastUsedAtUnixNano = nowUnixNano
	}
	res.payloadMu.Unlock()
}

func calcEgraphTermDigest(selfDigest digest.Digest, inputEqIDs []eqClassID) string {
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputEqIDs {
		h = h.WithDelim().
			WithUint64(uint64(in))
	}
	return h.DigestAndClose()
}

func compareEgraphTermID(a, b egraphTermID) int {
	return cmp.Compare(a, b)
}

func compareSharedResultID(a, b sharedResultID) int {
	return cmp.Compare(a, b)
}

func newEgraphTermIDSet() *set.TreeSet[egraphTermID] {
	return set.NewTreeSet(compareEgraphTermID)
}

func newSharedResultIDSet() *set.TreeSet[sharedResultID] {
	return set.NewTreeSet(compareSharedResultID)
}

func (c *Cache) ensureTermInputEqIDsLocked(ctx context.Context, inputDigests []digest.Digest) []eqClassID {
	inputEqIDs := make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		inputEqIDs[i] = c.findEqClassLocked(c.ensureEqClassForDigestLocked(ctx, inDig.String()))
	}
	return inputEqIDs
}

func (c *Cache) inputProvenanceForRefs(inputRefs []ResultCallStructuralInputRef) ([]egraphInputProvenanceKind, error) {
	inputProvenance := make([]egraphInputProvenanceKind, 0, len(inputRefs))
	for _, ref := range inputRefs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		switch {
		case ref.Result != nil:
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

func (c *Cache) initEgraphLocked() {
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
	if c.termResults == nil {
		c.termResults = make(map[egraphTermID]map[sharedResultID]egraphResultTermAssoc)
	}
	if c.resultTerms == nil {
		c.resultTerms = make(map[sharedResultID]map[egraphTermID]struct{})
	}
	if c.egraphTerms == nil {
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
	}
	if c.egraphTermsByTermDigest == nil {
		c.egraphTermsByTermDigest = make(map[string]*set.TreeSet[egraphTermID])
	}
	if c.egraphResultsByDigest == nil {
		c.egraphResultsByDigest = make(map[string]*set.TreeSet[sharedResultID])
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

func (c *Cache) ensureEqClassForDigestLocked(ctx context.Context, dig string) eqClassID {
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

func (c *Cache) findEqClassLocked(id eqClassID) eqClassID {
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

func (c *Cache) mergeEqClassesNoRepairLocked(a, b eqClassID) eqClassID {
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

func (c *Cache) mergeEqClassesLocked(ctx context.Context, ids ...eqClassID) eqClassID {
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

func (c *Cache) repairClassTermsLocked(ctx context.Context, root eqClassID) (merges []eqMergePair) {
	termSet := c.inputEqClassToTerms[root]
	if len(termSet) == 0 {
		return nil
	}
	traceEnabled := c.traceEnabled()

	termIDs := make([]egraphTermID, 0, len(termSet))
	for termID := range termSet {
		termIDs = append(termIDs, termID)
	}
	processedTermDigests := make(map[string]struct{}, len(termIDs))

	for _, termID := range termIDs {
		term := c.egraphTerms[termID]
		if term == nil {
			delete(termSet, termID)
			continue
		}

		oldInputs := term.inputEqIDs
		var newInputs []eqClassID
		inputsChanged := false
		for i, in := range oldInputs {
			rootIn := c.findEqClassLocked(in)
			if rootIn == in {
				continue
			}
			if !inputsChanged {
				newInputs = append([]eqClassID(nil), oldInputs...)
				inputsChanged = true
			}
			newInputs[i] = rootIn
		}

		if inputsChanged {
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

			oldTermDigest := term.termDigest
			newTermDigest := calcEgraphTermDigest(term.selfDigest, newInputs)
			if set := c.egraphTermsByTermDigest[term.termDigest]; set != nil {
				set.Remove(termID)
				if set.Empty() {
					delete(c.egraphTermsByTermDigest, term.termDigest)
				}
			}
			set := c.egraphTermsByTermDigest[newTermDigest]
			if set == nil {
				set = newEgraphTermIDSet()
				c.egraphTermsByTermDigest[newTermDigest] = set
			}
			set.Insert(termID)
			term.termDigest = newTermDigest

			if traceEnabled {
				oldInputsCopy := append([]eqClassID(nil), oldInputs...)
				c.traceTermInputsRepaired(ctx, term.id, oldInputsCopy, newInputs)
				c.traceTermRehomedUnderEqClasses(ctx, term.id, newInputs)
				c.traceTermDigestRecomputed(ctx, term.id, oldTermDigest, newTermDigest)
			}
		}

		processedTermDigests[term.termDigest] = struct{}{}
	}

	for termDigest := range processedTermDigests {
		set := c.egraphTermsByTermDigest[termDigest]
		if set == nil || set.Size() <= 1 {
			continue
		}

		var first *egraphTerm
		for otherID := range set.Items() {
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
		for otherID := range set.Items() {
			other := c.egraphTerms[otherID]
			if other == nil || other.id == first.id {
				continue
			}
			if traceEnabled {
				c.traceTermOutputsMerged(ctx, first.id, other.id, first.outputEqID, other.outputEqID)
			}
			merges = append(merges, eqMergePair{
				a: first.outputEqID,
				b: other.outputEqID,
			})
		}
	}
	return merges
}

func (c *Cache) firstLiveTermInSetLocked(termSet *set.TreeSet[egraphTermID]) *egraphTerm {
	if termSet == nil {
		return nil
	}
	for termID := range termSet.Items() {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		return term
	}
	return nil
}

func (c *Cache) firstResultDeterministicallyAtLocked(
	resultSet *set.TreeSet[sharedResultID],
	nowUnix int64,
) *sharedResult {
	if resultSet == nil {
		return nil
	}
	for resID := range resultSet.Items() {
		res := c.resultsByID[resID]
		if res == nil {
			continue
		}
		if c.resultExpiredAtLocked(res, nowUnix) {
			continue
		}
		return res
	}
	return nil
}

func (c *Cache) firstResultForOutputEqClassDeterministicallyAtLocked(
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

func (c *Cache) resultExpiredAtLocked(res *sharedResult, nowUnix int64) bool {
	if res == nil || res.expiresAtUnix == 0 {
		return false
	}
	return nowUnix >= res.expiresAtUnix
}

type lookupMatch struct {
	selfDigest            digest.Digest
	inputDigests          []digest.Digest
	inputEqIDs            []eqClassID
	primaryLookupPossible bool
	missingInputIndex     int
	hitRecipeDigest       bool
	candidates            *set.TreeSet[*sharedResult]
	termDigest            string
	termSetSize           int
}

func newSharedResultSet() *set.TreeSet[*sharedResult] {
	return set.NewTreeSet(compareSharedResults)
}

func (c *Cache) appendDigestResultsLocked(candidates *set.TreeSet[*sharedResult], dig digest.Digest, nowUnix int64) {
	if dig == "" {
		return
	}
	resultSet := c.egraphResultsByDigest[dig.String()]
	if resultSet == nil {
		return
	}
	for resID := range resultSet.Items() {
		res := c.resultsByID[resID]
		if res == nil || c.resultExpiredAtLocked(res, nowUnix) {
			continue
		}
		candidates.Insert(res)
	}
}

func (c *Cache) appendTermSetResultsLocked(candidates *set.TreeSet[*sharedResult], termSet *set.TreeSet[egraphTermID], nowUnix int64) {
	if termSet == nil {
		return
	}

	for termID := range termSet.Items() {
		for resID := range c.termResults[termID] {
			res := c.resultsByID[resID]
			if res == nil || c.resultExpiredAtLocked(res, nowUnix) {
				continue
			}
			candidates.Insert(res)
		}
	}
	if !candidates.Empty() {
		return
	}

	seenOutputEqClasses := make(map[eqClassID]struct{}, termSet.Size())
	for termID := range termSet.Items() {
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
		for dig := range c.eqClassToDigests[outputEqID] {
			c.appendDigestResultsLocked(candidates, digest.Digest(dig), nowUnix)
		}
	}
}

func (c *Cache) sessionSatisfiesResourceRequirementsLocked(sessionID string, res *sharedResult) bool {
	if res == nil || res.requiredSessionResources == nil || res.requiredSessionResources.Empty() {
		return true
	}

	c.sessionMu.Lock()
	available := c.sessionHandlesBySession[sessionID]
	c.sessionMu.Unlock()
	if available == nil {
		return false
	}
	return available.Subset(res.requiredSessionResources)
}

func (c *Cache) selectLookupCandidateForSessionLocked(sessionID string, candidates *set.TreeSet[*sharedResult]) *sharedResult {
	if candidates == nil {
		return nil
	}
	for res := range candidates.Items() {
		if c.sessionSatisfiesResourceRequirementsLocked(sessionID, res) {
			return res
		}
	}
	return nil
}

func (c *Cache) lookupMatchForDigestsLocked(recipeDigest digest.Digest, extraDigests []call.ExtraDigest, nowUnix int64) lookupMatch {
	match := lookupMatch{
		primaryLookupPossible: true,
		missingInputIndex:     -1,
	}
	if recipeDigest == "" {
		return match
	}

	candidates := newSharedResultSet()
	c.appendDigestResultsLocked(candidates, recipeDigest, nowUnix)
	if !candidates.Empty() {
		match.candidates = candidates
		match.hitRecipeDigest = true
		return match
	}
	for _, extra := range extraDigests {
		c.appendDigestResultsLocked(candidates, extra.Digest, nowUnix)
	}
	if !candidates.Empty() {
		match.candidates = candidates
	}
	return match
}

func (c *Cache) lookupMatchForCallLocked(
	ctx context.Context,
	frame *ResultCall,
	recipeDigest digest.Digest,
	selfDigest digest.Digest,
	inputDigests []digest.Digest,
	nowUnix int64,
) (lookupMatch, error) {
	match := lookupMatch{
		primaryLookupPossible: true,
		missingInputIndex:     -1,
	}
	if frame == nil {
		return match, nil
	}

	match = c.lookupMatchForDigestsLocked(recipeDigest, frame.ExtraDigests, nowUnix)
	if match.candidates != nil && !match.candidates.Empty() {
		return match, nil
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
		if termSet != nil {
			match.termSetSize = termSet.Size()
		}
		candidates := newSharedResultSet()
		c.appendTermSetResultsLocked(candidates, termSet, nowUnix)
		if !candidates.Empty() {
			match.candidates = candidates
		}
	}
	return match, nil
}

func (c *Cache) indexResultDigestsLocked(res *sharedResult, requestFrame, responseFrame *ResultCall) error {
	if res == nil {
		return nil
	}
	c.initEgraphLocked()

	indexDigest := func(dig digest.Digest) {
		if dig == "" {
			return
		}
		set := c.egraphResultsByDigest[dig.String()]
		if set == nil {
			set = newSharedResultIDSet()
			c.egraphResultsByDigest[dig.String()] = set
		}
		set.Insert(res.id)
	}

	indexFrame := func(frame *ResultCall) error {
		if frame == nil {
			return nil
		}
		dig, err := frame.deriveRecipeDigest(c)
		if err != nil {
			return err
		}
		indexDigest(dig)
		for _, extra := range frame.ExtraDigests {
			indexDigest(extra.Digest)
		}
		return nil
	}
	if err := indexFrame(requestFrame); err != nil {
		return fmt.Errorf("index request digests: %w", err)
	}
	if err := indexFrame(responseFrame); err != nil {
		return fmt.Errorf("index response digests: %w", err)
	}
	return nil
}

func (c *Cache) removeResultDigestsLocked(resID sharedResultID, outputEqClasses map[eqClassID]struct{}) {
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
			set.Remove(resID)
			if set.Empty() {
				delete(c.egraphResultsByDigest, dig)
			}
		}
	}
}

// lookupCacheForRequestLocked checks if the given call ID has an equivalent result in the cache.
// It first attempts direct digest lookup using the request recipe/extra digests. If that misses,
// it falls back to the canonical term lookup using (self, input eq-classes).
//
// This method assumes egraphMu is already held by the caller.
func (c *Cache) lookupCacheForRequestLocked(
	ctx context.Context,
	sessionID string,
	req *CallRequest,
	requestDigest digest.Digest,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []ResultCallStructuralInputRef,
) (AnyResult, bool, error) {
	if req == nil || req.ResultCall == nil {
		return nil, false, nil
	}
	now := time.Now()
	nowUnix := now.Unix()
	match, err := c.lookupMatchForCallLocked(ctx, req.ResultCall, requestDigest, requestSelf, requestInputs, nowUnix)
	if err != nil {
		return nil, false, err
	}
	c.traceLookupAttempt(ctx, requestDigest.String(), match.selfDigest.String(), match.inputDigests, req.IsPersistable)
	hitRes := c.selectLookupCandidateForSessionLocked(sessionID, match.candidates)

	if hitRes == nil {
		c.traceLookupMissNoMatch(ctx, requestDigest.String(), match.primaryLookupPossible, match.missingInputIndex, match.termDigest, match.termSetSize)
		return nil, false, nil
	}

	// fast-path: if we got a very simple recipe-digest hit we can skip trying to teach the egraph anything new
	if requestDigest != "" && len(req.ResultCall.ExtraDigests) == 0 && req.TTL == 0 && !req.IsPersistable && match.hitRecipeDigest {
		touchSharedResultLastUsed(hitRes, now.UnixNano())
		retRes := Result[Typed]{
			shared:   hitRes,
			hitCache: true,
		}
		c.traceLookupHit(ctx, requestDigest.String(), hitRes, match.termDigest)
		return retRes, true, nil
	}

	// We have a cache hit. Teach this request identity onto the existing shared
	// result so any raw ID we hand back is itself resolvable by the cache later.
	res := hitRes
	// A TTL-bearing call can alias an existing result on lookup; apply the same
	// conservative expiry merge policy here so TTL remains effective on hits.
	res.expiresAtUnix = mergeSharedResultExpiryUnix(
		res.expiresAtUnix,
		candidateSharedResultExpiryUnix(nowUnix, req.TTL),
	)
	touchSharedResultLastUsed(res, now.UnixNano())
	if req.IsPersistable {
		c.upsertPersistedEdgeLocked(ctx, res, candidateSharedResultExpiryUnix(nowUnix, req.TTL), false)
	}
	if err := c.teachResultIdentityLocked(ctx, res, req.ResultCall, requestDigest, requestSelf, requestInputs, requestInputRefs); err != nil {
		return nil, false, err
	}
	retRes := Result[Typed]{
		shared:   res,
		hitCache: true,
	}
	c.traceLookupHit(ctx, requestDigest.String(), res, match.termDigest)
	return retRes, true, nil
}

func (c *Cache) lookupCacheForRequest(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	req *CallRequest,
	requestDigest digest.Digest,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []ResultCallStructuralInputRef,
) (AnyResult, bool, error) {
	if sessionID == "" {
		return nil, false, errors.New("lookup cache for request: empty session ID")
	}
	if resolver == nil {
		return nil, false, errors.New("lookup cache for request: type resolver is nil")
	}
	if req == nil || req.ResultCall == nil {
		return nil, false, nil
	}

	c.egraphMu.Lock()
	retRes, hit, err := c.lookupCacheForRequestLocked(ctx, sessionID, req, requestDigest, requestSelf, requestInputs, requestInputRefs)
	if err != nil || !hit {
		c.egraphMu.Unlock()
		return retRes, hit, err
	}

	hitShared := retRes.cacheSharedResult()
	if hitShared == nil || hitShared.id == 0 {
		c.egraphMu.Unlock()
		return nil, false, fmt.Errorf("lookup cache for request: hit missing shared result ID")
	}

	trackedCount := 0
	alreadyTracked := false
	c.sessionMu.Lock()
	if c.sessionResultIDsBySession == nil {
		c.sessionResultIDsBySession = make(map[string]map[sharedResultID]struct{})
	}
	if c.sessionResultIDsBySession[sessionID] == nil {
		c.sessionResultIDsBySession[sessionID] = make(map[sharedResultID]struct{})
	}
	if _, found := c.sessionResultIDsBySession[sessionID][hitShared.id]; found {
		alreadyTracked = true
	} else {
		c.sessionResultIDsBySession[sessionID][hitShared.id] = struct{}{}
		c.incrementIncomingOwnershipLocked(ctx, hitShared)
	}
	trackedCount = len(c.sessionResultIDsBySession[sessionID])
	c.sessionMu.Unlock()
	c.egraphMu.Unlock()

	loadedHit, err := c.ensurePersistedHitValueLoaded(ctx, resolver, retRes)
	if err != nil {
		c.egraphMu.Lock()
		c.sessionMu.Lock()
		if resultIDs := c.sessionResultIDsBySession[sessionID]; resultIDs != nil {
			delete(resultIDs, hitShared.id)
			if len(resultIDs) == 0 {
				delete(c.sessionResultIDsBySession, sessionID)
			}
		}
		c.sessionMu.Unlock()
		queue := []*sharedResult(nil)
		var decErr error
		if !alreadyTracked {
			queue, decErr = c.decrementIncomingOwnershipLocked(ctx, hitShared, nil)
		}
		collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
		c.egraphMu.Unlock()
		return nil, false, errors.Join(err, decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases))
	}

	if c.traceEnabled() {
		c.traceSessionResultTracked(ctx, sessionID, loadedHit, true, trackedCount)
	}
	return loadedHit, true, nil
}

func (c *Cache) TeachCallEquivalentToResult(ctx context.Context, sessionID string, frame *ResultCall, res AnyResult) error {
	if frame == nil {
		return fmt.Errorf("teach call equivalence: nil call")
	}
	if res == nil {
		return fmt.Errorf("teach call equivalence: nil result")
	}

	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		if sessionID == "" {
			return fmt.Errorf("teach call equivalence: empty session ID for detached result")
		}
		attached, err := c.AttachResult(ctx, sessionID, CurrentDagqlServer(ctx), res)
		if err != nil {
			return fmt.Errorf("teach call equivalence: attach result: %w", err)
		}
		res = attached
		shared = res.cacheSharedResult()
	}
	if shared == nil || shared.id == 0 {
		return fmt.Errorf("teach call equivalence: target result missing shared result ID")
	}

	requestDigest, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return fmt.Errorf("teach call equivalence: derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := frame.selfDigestAndInputRefs(c)
	if err != nil {
		return fmt.Errorf("teach call equivalence: derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return fmt.Errorf("teach call equivalence: derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.teachResultIdentityLocked(ctx, shared, frame, requestDigest, requestSelf, requestInputs, requestInputRefs)
}

func (c *Cache) TeachContentDigest(ctx context.Context, res AnyResult, contentDigest digest.Digest) error {
	if res == nil {
		return fmt.Errorf("teach content digest: nil result")
	}
	if contentDigest == "" {
		return fmt.Errorf("teach content digest: empty digest")
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return fmt.Errorf("teach content digest: result %T is not an attached result in this cache", res)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		baseFrame := shared.loadResultCall()
		if baseFrame == nil {
			return fmt.Errorf("teach content digest: result %T has no call frame", res)
		}
		oldContentDigest := baseFrame.ContentDigest()
		frame := baseFrame.fork()

		replaced := false
		for i, extra := range frame.ExtraDigests {
			if extra.Label != call.ExtraDigestLabelContent {
				continue
			}
			frame.ExtraDigests[i].Digest = contentDigest
			replaced = true
			break
		}
		if !replaced {
			frame.ExtraDigests = append(frame.ExtraDigests, call.ExtraDigest{
				Label:  call.ExtraDigestLabelContent,
				Digest: contentDigest,
			})
		}

		requestDigest, err := frame.deriveRecipeDigest(c)
		if err != nil {
			return fmt.Errorf("teach content digest: derive request digest: %w", err)
		}
		requestSelf, requestInputRefs, err := frame.selfDigestAndInputRefs(c)
		if err != nil {
			return fmt.Errorf("teach content digest: derive request term digests: %w", err)
		}
		requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
		for _, ref := range requestInputRefs {
			dig, err := ref.inputDigest(c)
			if err != nil {
				return fmt.Errorf("teach content digest: derive request term input digest: %w", err)
			}
			requestInputs = append(requestInputs, dig)
		}

		c.egraphMu.Lock()
		shared = c.resultsByID[shared.id]
		if shared == nil {
			c.egraphMu.Unlock()
			return fmt.Errorf("teach content digest: result %T missing from cache", res)
		}
		if shared.loadResultCall() == nil {
			c.egraphMu.Unlock()
			return fmt.Errorf("teach content digest: result %T has no call frame", res)
		}
		if shared.loadResultCall() != baseFrame {
			c.egraphMu.Unlock()
			continue
		}
		c.traceTeachContentDigest(ctx, shared, oldContentDigest.String(), contentDigest.String(), requestDigest.String(), requestSelf.String(), requestInputs, frame)
		if err := c.teachResultIdentityLocked(ctx, shared, frame, requestDigest, requestSelf, requestInputs, requestInputRefs); err != nil {
			c.egraphMu.Unlock()
			return err
		}
		shared.storeResultCall(frame)
		c.traceResultCallFrameUpdated(ctx, shared, "teach_content_digest", baseFrame, frame)
		c.egraphMu.Unlock()
		return nil
	}
}

func (c *Cache) resultIDForCall(ctx context.Context, frame *ResultCall) (sharedResultID, error) {
	if frame == nil {
		return 0, fmt.Errorf("resolve result ID for call: nil call")
	}

	requestDigest, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return 0, fmt.Errorf("resolve result ID for call: derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := frame.selfDigestAndInputRefs(c)
	if err != nil {
		return 0, fmt.Errorf("resolve result ID for call: derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return 0, fmt.Errorf("resolve result ID for call: derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	match, err := c.lookupMatchForCallLocked(ctx, frame, requestDigest, requestSelf, requestInputs, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	if match.candidates == nil || match.candidates.Empty() {
		return 0, fmt.Errorf("resolve result ID for call: no attached result for %s", requestDigest)
	}
	for hitRes := range match.candidates.Items() {
		if hitRes != nil && hitRes.id != 0 {
			return hitRes.id, nil
		}
	}
	return 0, fmt.Errorf("resolve result ID for call: no attached result for %s", requestDigest)
}

func (c *Cache) termForResultByDigestLocked(resID sharedResultID, termDigest string) *egraphTerm {
	for termID := range c.resultTerms[resID] {
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

func (c *Cache) outputEqClassesForResultLocked(resID sharedResultID) map[eqClassID]struct{} {
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

func (c *Cache) termIDsForResultLocked(resID sharedResultID) map[egraphTermID]struct{} {
	termIDs := c.resultTerms[resID]
	if len(termIDs) == 0 {
		return nil
	}
	out := make(map[egraphTermID]struct{}, len(termIDs))
	for termID := range termIDs {
		if c.egraphTerms[termID] == nil {
			continue
		}
		out[termID] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (c *Cache) mergeOutputsForTermDigestLocked(ctx context.Context, termDigest string, outputEqID eqClassID) eqClassID {
	set := c.egraphTermsByTermDigest[termDigest]
	if set == nil || set.Empty() {
		return c.findEqClassLocked(outputEqID)
	}

	root := c.findEqClassLocked(outputEqID)
	if root == 0 {
		return 0
	}

	mergeIDs := []eqClassID{root}
	allSameRoot := true
	for termID := range set.Items() {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		termRoot := c.findEqClassLocked(term.outputEqID)
		if termRoot == 0 {
			continue
		}
		if termRoot != root {
			allSameRoot = false
		}
		mergeIDs = append(mergeIDs, termRoot)
	}
	if allSameRoot {
		for termID := range set.Items() {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			term.outputEqID = root
		}
		return root
	}
	root = c.mergeEqClassesLocked(ctx, mergeIDs...)
	for termID := range set.Items() {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		term.outputEqID = root
	}
	return root
}

func (c *Cache) associateResultWithTermLocked(
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
	}

	termResults := c.termResults[termID]
	if termResults == nil {
		termResults = make(map[sharedResultID]egraphResultTermAssoc)
		c.termResults[termID] = termResults
	}
	resultTerms := c.resultTerms[res.id]
	if resultTerms == nil {
		resultTerms = make(map[egraphTermID]struct{})
		c.resultTerms[res.id] = resultTerms
	}
	assoc, ok := termResults[res.id]
	if ok {
		if !sameInputProvenance(assoc.inputProvenance, inputProvenance) {
			assoc.inputProvenance = slices.Clone(inputProvenance)
			termResults[res.id] = assoc
			c.traceResultTermAssocUpdated(ctx, res.id, termID, inputProvenance)
		}
		return
	}
	termResults[res.id] = egraphResultTermAssoc{
		inputProvenance: slices.Clone(inputProvenance),
	}
	resultTerms[termID] = struct{}{}
	c.traceResultTermAssocAdded(ctx, res.id, termID, inputProvenance)
}

func (c *Cache) teachResultIdentityLocked(
	ctx context.Context,
	res *sharedResult,
	requestFrame *ResultCall,
	requestDigest digest.Digest,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []ResultCallStructuralInputRef,
) error {
	if res == nil || res.id == 0 || requestFrame == nil {
		return nil
	}
	c.initEgraphLocked()

	rootSet := c.outputEqClassesForResultLocked(res.id)
	if rootSet == nil {
		rootSet = make(map[eqClassID]struct{})
	}
	if requestDigest != "" {
		if eqID := c.ensureEqClassForDigestLocked(ctx, requestDigest.String()); eqID != 0 {
			rootSet[c.findEqClassLocked(eqID)] = struct{}{}
		}
	}
	for _, extra := range requestFrame.ExtraDigests {
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
	c.traceTeachResultIdentityRootSet(ctx, res, requestDigest.String(), requestSelf.String(), requestInputs, requestFrame, mergeIDs)
	outputEqID := c.mergeEqClassesLocked(ctx, mergeIDs...)
	if outputEqID == 0 {
		return nil
	}

	inputEqIDs := c.ensureTermInputEqIDsLocked(ctx, requestInputs)
	termDigest := calcEgraphTermDigest(requestSelf, inputEqIDs)
	existingTerm := c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest[termDigest])

	switch {
	case c.termForResultByDigestLocked(res.id, termDigest) != nil:
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
	case existingTerm != nil:
		inputProvenance, err := c.inputProvenanceForRefs(requestInputRefs)
		if err != nil {
			return fmt.Errorf("derive input provenance for request term %s: %w", requestSelf, err)
		}
		c.associateResultWithTermLocked(ctx, res, existingTerm.id, inputProvenance)
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
	default:
		mergedOutputEqID := c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
		inputProvenance, err := c.inputProvenanceForRefs(requestInputRefs)
		if err != nil {
			return fmt.Errorf("derive input provenance for request term %s: %w", requestSelf, err)
		}

		termID := c.nextEgraphTermID
		c.nextEgraphTermID++

		newTerm := newEgraphTerm(termID, requestSelf, inputEqIDs, mergedOutputEqID)
		c.egraphTerms[termID] = newTerm
		c.traceTermCreated(ctx, "runtime", "", newTerm)

		digestTerms := c.egraphTermsByTermDigest[newTerm.termDigest]
		if digestTerms == nil {
			digestTerms = newEgraphTermIDSet()
			c.egraphTermsByTermDigest[newTerm.termDigest] = digestTerms
		}
		digestTerms.Insert(termID)

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

	if err := c.indexResultDigestsLocked(res, requestFrame, nil); err != nil {
		return err
	}
	for termID := range c.resultTerms[res.id] {
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
		for _, extra := range requestFrame.ExtraDigests {
			if extra.Digest == "" {
				continue
			}
			extras[extra] = struct{}{}
		}
	}

	return nil
}

func (c *Cache) indexWaitResultInEgraphLocked(
	ctx context.Context,
	requestFrame *ResultCall,
	responseFrame *ResultCall,
	requestDigest digest.Digest,
	responseDigest digest.Digest,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []ResultCallStructuralInputRef,
	resultTermSelf digest.Digest,
	resultTermInputs []digest.Digest,
	resultTermInputRefs []ResultCallStructuralInputRef,
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
	if requestFrame != nil {
		addDigest(requestDigest.String())
		for _, extra := range requestFrame.ExtraDigests {
			addDigest(extra.Digest.String())
		}
	}
	if responseFrame != nil {
		addDigest(responseDigest.String())
		for _, extra := range responseFrame.ExtraDigests {
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
	if res.loadResultCall() == nil && requestFrame != nil {
		res.storeResultCall(requestFrame.clone())
		c.traceResultCallFrameUpdated(ctx, res, "index_wait_result_request_frame", nil, res.loadResultCall())
	}
	c.traceResultCreated(ctx, res)

	//
	// associate the result with the relevant terms and apply any necessary eq class unions + repairs
	//

	termsToIndex := []struct {
		selfDigest   digest.Digest
		inputDigests []digest.Digest
		inputRefs    []ResultCallStructuralInputRef
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
			inputRefs    []ResultCallStructuralInputRef
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
			digestTerms = newEgraphTermIDSet()
			c.egraphTermsByTermDigest[newTerm.termDigest] = digestTerms
		}
		digestTerms.Insert(termID)

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

	if err := c.indexResultDigestsLocked(res, requestFrame, responseFrame); err != nil {
		return err
	}
	for termID := range c.resultTerms[res.id] {
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
		if requestFrame != nil {
			for _, extra := range requestFrame.ExtraDigests {
				if extra.Digest == "" {
					continue
				}
				extras[extra] = struct{}{}
			}
		}
		if responseFrame != nil {
			for _, extra := range responseFrame.ExtraDigests {
				if extra.Digest == "" {
					continue
				}
				extras[extra] = struct{}{}
			}
		}
	}

	return nil
}

func (c *Cache) removeResultFromEgraphLocked(ctx context.Context, res *sharedResult) error {
	if res == nil {
		return nil
	}
	if len(c.egraphTerms) == 0 || len(c.resultOutputEqClasses) == 0 {
		c.maybeResetEgraphLocked()
		return nil
	}

	affectedOutputEqClasses := c.outputEqClassesForResultLocked(res.id)
	for termID := range c.resultTerms[res.id] {
		if termResults := c.termResults[termID]; termResults != nil {
			delete(termResults, res.id)
			if len(termResults) == 0 {
				delete(c.termResults, termID)
			}
		}
		c.traceResultTermAssocRemoved(ctx, res.id, termID)
	}
	delete(c.resultTerms, res.id)
	c.removeResultDigestsLocked(res.id, affectedOutputEqClasses)
	delete(c.resultOutputEqClasses, res.id)
	oldFrame := res.loadResultCall()
	depCount := len(res.deps)
	res.storeResultCall(nil)
	delete(c.resultsByID, res.id)
	c.traceResultRemoved(ctx, res, oldFrame, depCount)

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
				set.Remove(termID)
				if set.Empty() {
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
			delete(c.termResults, termID)
			delete(c.termInputProvenance, termID)
			c.traceTermRemoved(ctx, termID)
		}
		delete(c.outputEqClassToTerms, outputEqID)
	}
	c.maybeResetEgraphLocked()
	return nil
}

func (c *Cache) maybeResetEgraphLocked() {
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
	c.termResults = nil
	c.resultTerms = nil
	c.egraphTerms = nil
	c.termInputProvenance = nil
	c.egraphTermsByTermDigest = nil
	c.egraphResultsByDigest = nil
	c.resultsByID = nil
	c.nextEgraphClassID = 0
	c.nextEgraphTermID = 0
	c.nextSharedResultID = 0
}

func (c *Cache) compactEqClassesLocked() (changed bool, oldSlots int, newSlots int) {
	if len(c.egraphParents) <= 1 {
		return false, 0, 0
	}

	liveRoots := make(map[eqClassID]struct{})
	for _, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		for _, inputEqID := range term.inputEqIDs {
			root := c.findEqClassLocked(inputEqID)
			if root != 0 {
				liveRoots[root] = struct{}{}
			}
		}
		root := c.findEqClassLocked(term.outputEqID)
		if root != 0 {
			liveRoots[root] = struct{}{}
		}
	}
	for _, outputEqIDs := range c.resultOutputEqClasses {
		for outputEqID := range outputEqIDs {
			root := c.findEqClassLocked(outputEqID)
			if root != 0 {
				liveRoots[root] = struct{}{}
			}
		}
	}

	oldSlots = len(c.egraphParents) - 1
	newSlots = len(liveRoots)
	if newSlots == 0 || oldSlots < newSlots*2 {
		return false, oldSlots, newSlots
	}

	oldRoots := make([]eqClassID, 0, len(liveRoots))
	for root := range liveRoots {
		oldRoots = append(oldRoots, root)
	}
	slices.Sort(oldRoots)

	remap := make(map[eqClassID]eqClassID, len(oldRoots))
	newParents := make([]eqClassID, len(oldRoots)+1)
	newRanks := make([]uint8, len(oldRoots)+1)
	for i, oldRoot := range oldRoots {
		newRoot := eqClassID(i + 1)
		remap[oldRoot] = newRoot
		newParents[newRoot] = newRoot
	}

	newEqClassToDigests := make(map[eqClassID]map[string]struct{}, len(oldRoots))
	newEqClassExtraDigests := make(map[eqClassID]map[call.ExtraDigest]struct{}, len(oldRoots))
	newEgraphDigestToClass := make(map[string]eqClassID, len(c.egraphDigestToClass))
	for _, oldRoot := range oldRoots {
		newRoot := remap[oldRoot]
		if oldDigests := c.eqClassToDigests[oldRoot]; len(oldDigests) > 0 {
			newDigests := make(map[string]struct{}, len(oldDigests))
			for dig := range oldDigests {
				newDigests[dig] = struct{}{}
				newEgraphDigestToClass[dig] = newRoot
			}
			newEqClassToDigests[newRoot] = newDigests
		}
		if oldExtras := c.eqClassExtraDigests[oldRoot]; len(oldExtras) > 0 {
			newExtras := make(map[call.ExtraDigest]struct{}, len(oldExtras))
			for extra := range oldExtras {
				newExtras[extra] = struct{}{}
			}
			newEqClassExtraDigests[newRoot] = newExtras
		}
	}

	newInputEqClassToTerms := make(map[eqClassID]map[egraphTermID]struct{})
	newOutputEqClassToTerms := make(map[eqClassID]map[egraphTermID]struct{})
	newEgraphTermsByTermDigest := make(map[string]*set.TreeSet[egraphTermID], len(c.egraphTermsByTermDigest))
	for termID, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		newInputEqIDs := make([]eqClassID, 0, len(term.inputEqIDs))
		for _, inputEqID := range term.inputEqIDs {
			root := c.findEqClassLocked(inputEqID)
			newRoot := remap[root]
			if newRoot == 0 {
				continue
			}
			newInputEqIDs = append(newInputEqIDs, newRoot)
			set := newInputEqClassToTerms[newRoot]
			if set == nil {
				set = make(map[egraphTermID]struct{})
				newInputEqClassToTerms[newRoot] = set
			}
			set[termID] = struct{}{}
		}
		term.inputEqIDs = newInputEqIDs

		outputRoot := c.findEqClassLocked(term.outputEqID)
		newOutputRoot := remap[outputRoot]
		if newOutputRoot == 0 {
			continue
		}
		term.outputEqID = newOutputRoot
		outputTerms := newOutputEqClassToTerms[newOutputRoot]
		if outputTerms == nil {
			outputTerms = make(map[egraphTermID]struct{})
			newOutputEqClassToTerms[newOutputRoot] = outputTerms
		}
		outputTerms[termID] = struct{}{}

		term.termDigest = calcEgraphTermDigest(term.selfDigest, term.inputEqIDs)
		digestTerms := newEgraphTermsByTermDigest[term.termDigest]
		if digestTerms == nil {
			digestTerms = newEgraphTermIDSet()
			newEgraphTermsByTermDigest[term.termDigest] = digestTerms
		}
		digestTerms.Insert(termID)
	}

	newResultOutputEqClasses := make(map[sharedResultID]map[eqClassID]struct{}, len(c.resultOutputEqClasses))
	for resID, outputEqIDs := range c.resultOutputEqClasses {
		newOutputEqIDs := make(map[eqClassID]struct{}, len(outputEqIDs))
		for outputEqID := range outputEqIDs {
			root := c.findEqClassLocked(outputEqID)
			newRoot := remap[root]
			if newRoot != 0 {
				newOutputEqIDs[newRoot] = struct{}{}
			}
		}
		if len(newOutputEqIDs) > 0 {
			newResultOutputEqClasses[resID] = newOutputEqIDs
		}
	}

	c.egraphParents = newParents
	c.egraphRanks = newRanks
	c.eqClassToDigests = newEqClassToDigests
	c.eqClassExtraDigests = newEqClassExtraDigests
	c.egraphDigestToClass = newEgraphDigestToClass
	c.inputEqClassToTerms = newInputEqClassToTerms
	c.outputEqClassToTerms = newOutputEqClassToTerms
	c.resultOutputEqClasses = newResultOutputEqClasses
	c.egraphTermsByTermDigest = newEgraphTermsByTermDigest
	c.nextEgraphClassID = eqClassID(len(newParents))

	return true, oldSlots, newSlots
}
