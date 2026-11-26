package dagql

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// egraphInputProvenanceKind is whether a term's input slot was result-backed
// or digest-only when the term was observed: one byte a slot, as a term's
// provenance is held for each of its inputs. Persistence and debug output
// name it as String does.
type egraphInputProvenanceKind uint8

const (
	egraphInputProvenanceKindResult egraphInputProvenanceKind = iota + 1
	egraphInputProvenanceKindDigest
)

// String is the kind's name as persistence stores it: "result" or
// "digest".
func (k egraphInputProvenanceKind) String() string {
	switch k {
	case egraphInputProvenanceKindResult:
		return "result"
	case egraphInputProvenanceKindDigest:
		return "digest"
	default:
		return fmt.Sprintf("provenance(%d)", uint8(k))
	}
}

// parseEgraphInputProvenanceKind is the kind String names name, and
// whether it names one.
func parseEgraphInputProvenanceKind(name string) (egraphInputProvenanceKind, bool) {
	switch name {
	case "result":
		return egraphInputProvenanceKindResult, true
	case "digest":
		return egraphInputProvenanceKindDigest, true
	default:
		return 0, false
	}
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
	// The last use is the results row's, not the saved record's.
	res.payloadMu.unlockUncounted()
}

func calcEgraphTermDigest(selfDigest digest.Digest, inputEqIDs []eqClassID) string {
	h := hashutil.NewHasher().WithString(selfDigest.String())
	for _, in := range inputEqIDs {
		h = h.WithDelim().
			WithUint64(uint64(in))
	}
	return h.DigestAndClose()
}

func compareSharedResultID(a, b sharedResultID) int {
	return cmp.Compare(a, b)
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
	if c.egraphUses == nil {
		c.egraphUses = []uint32{0}
	}
	if c.eqClassToDigests == nil {
		c.eqClassToDigests = make(map[eqClassID]*idSet[string])
	}
	if c.eqClassExtraDigests == nil {
		c.eqClassExtraDigests = make(map[eqClassID][]call.ExtraDigest)
	}
	if c.egraphTerms == nil {
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
	}
	if c.resultIndexedDigests == nil {
		c.resultIndexedDigests = make(map[sharedResultID][]string)
	}
	if c.broadlyIndexedResults == nil {
		c.broadlyIndexedResults = make(map[sharedResultID]struct{})
	}
	if c.termInputProvenance == nil {
		c.termInputProvenance = make(map[egraphTermID][]egraphInputProvenanceKind)
	}
	if c.resultsByID == nil {
		c.resultsByID = make(map[sharedResultID]*sharedResult)
	}
	if c.nextEgraphTermID == 0 {
		c.nextEgraphTermID = 1
	}
	c.nextSharedResultID.CompareAndSwap(0, 1)
}

func (c *Cache) ensureEqClassForDigestLocked(ctx context.Context, dig string) eqClassID {
	if dig == "" {
		return 0
	}
	c.initEgraphLocked()
	if id, ok := c.egraphDigestToClass[dig]; ok {
		return c.findEqClassLocked(id)
	}
	id := c.allocEqClassLocked()
	c.egraphDigestToClass[dig] = id
	c.forgetCallTermsLocked(dig)
	digests := c.eqClassToDigests[id]
	if digests == nil {
		digests = newIDSet[string]()
		c.eqClassToDigests[id] = digests
	}
	digests.Insert(dig)
	c.traceEqClassCreated(ctx, id, dig)
	return id
}

// allocEqClassLocked returns a number for a new class: the last a freed
// class left (freeEndedEqClassesLocked), or the next after every slot. The
// class is noted as ended until a term or an entry uses it: one made by an
// operation that stops before using it is freed by the next collection.
// Requires egraphMu for writing, and an initialized e-graph.
func (c *Cache) allocEqClassLocked() eqClassID {
	var id eqClassID
	// While a save reads the store, a freed number isn't taken again: a
	// number the save has read names one class until it ends.
	if n := len(c.freeEqClassIDs); n > 0 && !c.savingLocked() {
		id = c.freeEqClassIDs[n-1]
		c.freeEqClassIDs = c.freeEqClassIDs[:n-1]
		c.egraphParents[id] = id
		c.egraphRanks[id] = 0
		c.egraphUses[id] = 0
	} else {
		id = eqClassID(len(c.egraphParents))
		c.egraphParents = append(c.egraphParents, id)
		c.egraphRanks = append(c.egraphRanks, 0)
		c.egraphUses = append(c.egraphUses, 0)
	}
	c.noteSavedClassLocked(id)
	c.endedEqClasses = append(c.endedEqClasses, id)
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

// unionRootsLocked orders two roots a union joins, the survivor first: the
// class more terms take as an input, then the one of higher rank, then ra.
// Keeping the class with more terms moves the fewest. The survivor takes a
// rank above the loser's when the loser's is as high as its own, which
// keeps a rank above its tree's height, up to the most a rank holds. With
// testRepairWholeClass, the survivor is the one of higher rank, then ra.
func (c *Cache) unionRootsLocked(ra, rb eqClassID) (survivor, loser eqClassID) {
	if c.testRepairWholeClass {
		if c.egraphRanks[ra] < c.egraphRanks[rb] {
			ra, rb = rb, ra
		}
	} else if na, nb := c.inputEqClassToTerms.Size(ra), c.inputEqClassToTerms.Size(rb); na < nb || (na == nb && c.egraphRanks[ra] < c.egraphRanks[rb]) {
		ra, rb = rb, ra
	}
	if r := c.egraphRanks[rb]; r >= c.egraphRanks[ra] {
		c.egraphRanks[ra] = min(r, math.MaxUint8-1) + 1
	}
	return ra, rb
}

// mergeEqClassesNoRepairLocked unions the classes of a and b and returns the
// surviving root (unionRootsLocked). absorbedInputTerms reports whether the
// union moved input terms from the losing root to the survivor, and moved,
// unless nil, takes each live term it moved: those terms still name the
// losing root until they are repaired. Everything else that names the losing
// root names the survivor once the union returns: its digests, the terms it
// was the output of, and its entries. So once the repair is done, only its
// parent link names it, for a number an operation still holds, and it is
// noted for the collector to free (freeEndedEqClassesLocked).
func (c *Cache) mergeEqClassesNoRepairLocked(a, b eqClassID, moved *[]egraphTermID) (root eqClassID, absorbedInputTerms bool) {
	ra := c.findEqClassLocked(a)
	rb := c.findEqClassLocked(b)
	if ra == 0 {
		return rb, false
	}
	if rb == 0 {
		return ra, false
	}
	if ra == rb {
		return ra, false
	}
	c.egraphUnions++

	ra, rb = c.unionRootsLocked(ra, rb)
	c.egraphParents[rb] = ra
	c.joinEqClassUsesLocked(ra, rb)
	// Every row that named rb names ra once resolved: the classes, the terms
	// that take or give rb and the entries of rb change for a save.
	c.noteSavedClassLocked(ra)
	c.noteSavedClassLocked(rb)

	// merge reverse digest indexes
	dstDigests := c.eqClassToDigests[ra]
	srcDigests := c.eqClassToDigests[rb]
	if srcDigests.Size() > 0 {
		if dstDigests == nil {
			dstDigests = newIDSet[string]()
			c.eqClassToDigests[ra] = dstDigests
		}
		for dig := range srcDigests.All() {
			dstDigests.Insert(dig)
			c.egraphDigestToClass[dig] = ra
			c.forgetCallTermsLocked(dig)
		}
		delete(c.eqClassToDigests, rb)
	}

	if srcExtraDigests := c.eqClassExtraDigests[rb]; len(srcExtraDigests) > 0 {
		c.eqClassExtraDigests[ra] = withExtraDigests(c.eqClassExtraDigests[ra], srcExtraDigests...)
		delete(c.eqClassExtraDigests, rb)
	}

	// merge reverse input-term indexes
	if srcInputTerms := c.inputEqClassToTerms.Members(rb); srcInputTerms.Size() > 0 {
		var gone []egraphTermID
		for termID := range srcInputTerms.All() {
			c.noteSavedTermLocked(termID)
			if moved != nil {
				// A term gone since it was indexed has nothing to repair:
				// it leaves the index here, as a repair of the whole class
				// drops it.
				if c.egraphTerms[termID] == nil {
					gone = append(gone, termID)
					continue
				}
				*moved = append(*moved, termID)
			}
		}
		for _, termID := range gone {
			c.inputEqClassToTerms.Remove(rb, termID)
		}
		c.inputEqClassToTerms.Move(rb, ra)
		absorbedInputTerms = true
	}

	// merge reverse output-term indexes
	if srcOutputTerms := c.outputEqClassToTerms.Members(rb); srcOutputTerms.Size() > 0 {
		for termID := range srcOutputTerms.All() {
			c.noteSavedTermLocked(termID)
			// The term's use moved with the class's (joinEqClassUsesLocked).
			if term := c.egraphTerms[termID]; term != nil {
				term.outputEqID = ra
			}
		}
		c.outputEqClassToTerms.Move(rb, ra)
	}

	// Merge result/output-class associations and eagerly rewrite the moved
	// results' forward entries to the winning canonical root.
	dstResults := c.outputEqClassResults.Members(ra)
	srcResults := c.outputEqClassResults.Members(rb)
	joinsEntries := dstResults.Size() > 0 && srcResults.Size() > 0
	if joinsEntries {
		// Every entry of both classes now answers from the other's holders
		// and borrows the other's stored parts: the join marks, for readers
		// and for the stamps of the bundles that borrow from the class, what
		// that changes, walking the smaller side (markJoinLocked).
		c.markJoinLocked(srcResults, dstResults, ra)
	}
	if srcResults.Size() > 0 {
		for resID := range srcResults.All() {
			c.noteSavedEntryLocked(resID)
			// An entry that named both classes names one now, which it
			// uses once.
			if c.resultOutputEqClasses.Rename(resID, rb, ra) {
				c.unuseEqClassLocked(ra)
			}
		}
		c.outputEqClassResults.Move(rb, ra)
	}
	c.endedEqClasses = append(c.endedEqClasses, rb)

	return ra, absorbedInputTerms
}

// mergeEqClassesLocked unions the classes of ids and repairs the terms the
// unions moved until no two terms share a digest across classes: a term
// whose inputs are now one class with another's is congruent to it, and
// their outputs are unioned in turn. It returns the root of ids[0]'s class.
//
// Only a term that named an absorbed class can change its inputs or its
// digest, so only such a term can come to share a digest with another: the
// surviving class's own terms already name roots, which every union's repair
// keeps true, as every term is made, imported and repaired naming roots.
// So the repair walks the terms each union moved, and a union into a class
// that many terms take as an input costs what the absorbed class had.
func (c *Cache) mergeEqClassesLocked(ctx context.Context, ids ...eqClassID) eqClassID {
	if len(ids) == 0 {
		return 0
	}
	if len(ids) == 1 {
		return c.findEqClassLocked(ids[0])
	}
	if c.testRepairWholeClass {
		return c.mergeEqClassesRepairingWholeClassLocked(ctx, ids...)
	}

	var moved []egraphTermID
	root := c.findEqClassLocked(ids[0])
	for _, id := range ids[1:] {
		root, _ = c.mergeEqClassesNoRepairLocked(root, id, &moved)
	}
	c.traceEqClassMerged(ctx, ids, root)

	// Repair until closure. A repair's congruent terms union their outputs,
	// which moves the absorbed class's terms in turn. Each union removes a
	// root for good, so the loop ends.
	for len(moved) > 0 {
		batch := moved
		moved = nil
		for _, pair := range c.repairTermsLocked(ctx, batch) {
			c.mergeEqClassesNoRepairLocked(pair.a, pair.b, &moved)
		}
	}
	return c.findEqClassLocked(ids[0])
}

// mergeEqClassesRepairingWholeClassLocked is mergeEqClassesLocked as it was
// before its repair walked only the terms its unions moved: each repair
// walks every term of the class that survives. Tests set
// testRepairWholeClass to compare the two.
func (c *Cache) mergeEqClassesRepairingWholeClassLocked(ctx context.Context, ids ...eqClassID) eqClassID {
	root := c.findEqClassLocked(ids[0])
	for _, id := range ids[1:] {
		root, _ = c.mergeEqClassesNoRepairLocked(root, id, nil)
	}
	c.traceEqClassMerged(ctx, ids, root)

	// Repair until closure. A root is repaired again only after a union moves
	// input terms into it, since those terms are still keyed by the losing
	// root. Each such union removes a root for good, so a root is repaired at
	// most once plus once per union that grew it, and the loop terminates.
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
			next, absorbedInputTerms := c.mergeEqClassesNoRepairLocked(pair.a, pair.b, nil)
			if next == 0 {
				continue
			}
			if absorbedInputTerms {
				delete(repaired, next)
			}
			toRepair = append(toRepair, next)
		}
	}
	return c.findEqClassLocked(ids[0])
}

// repairTermsLocked repairs the terms a union moved (repairTermLocked), and
// returns a merge for each term that now shares a digest with another: their
// outputs are one class. A term walked twice repairs once.
func (c *Cache) repairTermsLocked(ctx context.Context, termIDs []egraphTermID) []eqMergePair {
	traceEnabled := c.traceEnabled()
	processedTermDigests := make(map[string]struct{}, len(termIDs))
	for _, termID := range termIDs {
		c.egraphRepairWalks++
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		c.repairTermLocked(ctx, term, traceEnabled)
		processedTermDigests[term.termDigest] = struct{}{}
	}
	return c.congruentTermMergesLocked(ctx, processedTermDigests, traceEnabled)
}

// repairClassTermsLocked repairs every term that takes root as an input,
// as mergeEqClassesRepairingWholeClassLocked does.
func (c *Cache) repairClassTermsLocked(ctx context.Context, root eqClassID) (merges []eqMergePair) {
	inputTerms := c.inputEqClassToTerms.Members(root)
	if inputTerms.Size() == 0 {
		return nil
	}
	traceEnabled := c.traceEnabled()

	termIDs := make([]egraphTermID, 0, inputTerms.Size())
	for termID := range inputTerms.All() {
		termIDs = append(termIDs, termID)
	}
	processedTermDigests := make(map[string]struct{}, len(termIDs))

	for _, termID := range termIDs {
		c.egraphRepairWalks++
		term := c.egraphTerms[termID]
		if term == nil {
			c.inputEqClassToTerms.Remove(root, termID)
			continue
		}
		c.repairTermLocked(ctx, term, traceEnabled)
		processedTermDigests[term.termDigest] = struct{}{}
	}
	return c.congruentTermMergesLocked(ctx, processedTermDigests, traceEnabled)
}

// repairTermLocked moves each of a term's inputs that is no longer a root
// to its root, in the input-term index and the term-digest index, with the
// term's digest made again from its new inputs.
func (c *Cache) repairTermLocked(ctx context.Context, term *egraphTerm, traceEnabled bool) {
	termID := term.id
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
	if !inputsChanged {
		return
	}

	for _, in := range oldInputs {
		c.inputEqClassToTerms.Remove(in, termID)
	}
	for _, in := range newInputs {
		if in == 0 {
			continue
		}
		c.inputEqClassToTerms.Insert(in, termID)
	}
	term.inputEqIDs = newInputs
	c.noteSavedTermLocked(termID)

	oldTermDigest := term.termDigest
	newTermDigest := calcEgraphTermDigest(term.selfDigest, newInputs)
	c.egraphTermsByTermDigest.Remove(term.termDigest, termID)
	c.egraphTermsByTermDigest.Insert(newTermDigest, termID)
	term.termDigest = newTermDigest

	if traceEnabled {
		oldInputsCopy := append([]eqClassID(nil), oldInputs...)
		c.traceTermInputsRepaired(ctx, term.id, oldInputsCopy, newInputs)
		c.traceTermRehomedUnderEqClasses(ctx, term.id, newInputs)
		c.traceTermDigestRecomputed(ctx, term.id, oldTermDigest, newTermDigest)
	}
}

// congruentTermMergesLocked returns, for each digest repaired terms now
// have, a merge of the first live term's output with every other's.
func (c *Cache) congruentTermMergesLocked(ctx context.Context, termDigests map[string]struct{}, traceEnabled bool) (merges []eqMergePair) {
	for termDigest := range termDigests {
		set := c.egraphTermsByTermDigest.Members(termDigest)
		if set.Size() <= 1 {
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

func (c *Cache) firstLiveTermInSetLocked(termSet idMembers[egraphTermID]) *egraphTerm {
	for termID := range termSet.Items() {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		return term
	}
	return nil
}

func (c *Cache) hasUnexpiredResultForOutputEqClassLocked(
	outputEqID eqClassID,
	nowUnix int64,
) bool {
	outputEqID = c.findEqClassLocked(outputEqID)
	if outputEqID == 0 {
		return false
	}
	for resID := range c.outputEqClassResults.All(outputEqID) {
		res := c.resultsByID[resID]
		if res == nil {
			continue
		}
		if res.hasUnexpiredHoldingLocked(nowUnix) {
			// Another cache's unexpired stored copy keeps the class's
			// terms: the equivalence stays usable while any value in it is.
			return true
		}
		if res.noValueLocked() || c.resultExpiredAtLocked(res, nowUnix) {
			continue
		}
		return true
	}
	return false
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
	// route records which index produced the current candidate set — the
	// cache-evidence hit provenance (CacheHitRouteRecipe/Digest/Structural).
	// Purely observational: candidate gathering and selection are unchanged.
	route CacheHitRoute
	// expired records what expiry dropped during candidate accumulation
	// (cache-evidence miss facts).
	expired expirySeen
}

// expirySeen records that expiry dropped at least one otherwise-matching
// result during candidate accumulation, and whether one of them was a value a
// Cloud merge installed. Cache-evidence miss facts only: candidate gathering
// is unchanged.
type expirySeen struct {
	any    bool
	remote bool
}

func newSharedResultSet() *set.TreeSet[*sharedResult] {
	return set.NewTreeSet(compareSharedResults)
}

// expired, when non-nil, records the results expiry drops during
// accumulation (cache-evidence miss facts); candidate gathering itself is
// unchanged.
func (c *Cache) appendDigestResultsLocked(candidates *set.TreeSet[*sharedResult], dig digest.Digest, nowUnix int64, expired *expirySeen) {
	if dig == "" {
		return
	}
	for resID := range c.egraphResultsByDigest.Items(dig.String()) {
		if res := c.servableCandidateLocked(resID, nowUnix, expired); res != nil {
			candidates.Insert(res)
		}
	}
}

// appendOutputEqClassResultsLocked adds the servable results of an output
// eq-class root, read from outputEqClassResults. It finds the same results
// as appendDigestResultsLocked over every digest of the class: a result is
// posted only under digests merged into one of its output classes, and every
// output class of a result holds a digest it is posted under. It visits each
// result once, where the digests' walk visits it once for every digest it is
// posted under. expired is filled as in appendDigestResultsLocked.
func (c *Cache) appendOutputEqClassResultsLocked(candidates *set.TreeSet[*sharedResult], root eqClassID, nowUnix int64, expired *expirySeen) {
	for resID := range c.outputEqClassResults.All(root) {
		if res := c.servableCandidateLocked(resID, nowUnix, expired); res != nil {
			candidates.Insert(res)
		}
	}
}

// servableCandidateLocked returns the result lookup may serve for resID, or
// nil. expired is filled as in appendDigestResultsLocked.
func (c *Cache) servableCandidateLocked(resID sharedResultID, nowUnix int64, expired *expirySeen) *sharedResult {
	if c.testServableVisited != nil {
		c.testServableVisited(resID)
	}
	res := c.resultsByID[resID]
	if res == nil {
		return nil
	}
	if res.attachmentState() == resultAttachmentFailed {
		return nil
	}
	// An entry known only through holdings has no value to serve.
	if res.noValueLocked() {
		return nil
	}
	if c.resultExpiredAtLocked(res, nowUnix) {
		if expired != nil {
			expired.any = true
			expired.remote = expired.remote || res.imported
		}
		return nil
	}
	return res
}

func (c *Cache) appendTermSetResultsLocked(candidates *set.TreeSet[*sharedResult], termSet idMembers[egraphTermID], nowUnix int64, expired *expirySeen) {
	if termSet.Size() == 0 {
		return
	}

	for termID := range termSet.Items() {
		for resID := range c.termResults.All(termID) {
			if res := c.servableCandidateLocked(resID, nowUnix, expired); res != nil {
				candidates.Insert(res)
			}
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
		outputEqID := c.eqClassRootLocked(term.outputEqID)
		if outputEqID == 0 {
			continue
		}
		if _, ok := seenOutputEqClasses[outputEqID]; ok {
			continue
		}
		seenOutputEqClasses[outputEqID] = struct{}{}
		c.appendOutputEqClassResultsLocked(candidates, outputEqID, nowUnix, expired)
	}
}

func (c *Cache) sessionSatisfiesResourceRequirementsLocked(sessionID string, res *sharedResult) bool {
	if res == nil || res.requiredSessionResources == nil || res.requiredSessionResources.Empty() {
		return true
	}

	// The set is immutable once published (BindSessionResource replaces it),
	// so it can be read after sessionMu is dropped.
	c.sessionMu.Lock()
	available := c.sessionHandlesBySession[sessionID]
	c.sessionMu.Unlock()
	if available == nil {
		return false
	}
	return available.Subset(res.requiredSessionResources)
}

// sessionStillSatisfiesResourceRequirements re-runs the lookup filter after
// a serve path left the selection critical section. The stored required set
// can grow after selection: an attached dep may carry requirements while
// the result's attachment is in flight, and an explicit retention edge
// (AddExplicitDependency) may land a requirement-carrying dep after the
// result settled. Every change to the stored set bumps the result's
// requirement generation inside the same egraphMu write critical section,
// so a serve whose selection-time capture still matches the current
// generation is serving the exact set the selection check validated and
// skips the locked re-check; on a mismatch the full locked subset check
// decides.
func (c *Cache) sessionStillSatisfiesResourceRequirements(ctx context.Context, sessionID string, res *sharedResult, requiredGenAtSelection uint64) bool {
	if res == nil {
		return true
	}
	if res.requiredSessionResourcesGen.Load() == requiredGenAtSelection {
		return true
	}
	read := c.lookupReadLockGraph(ctx)
	defer read.Unlock()
	return c.sessionSatisfiesResourceRequirementsLocked(sessionID, res)
}

// selectLookupCandidateForSessionLocked returns the first candidate the
// session may use, or one bound to the session (BindToSession) when there is
// one: that value is the session's own outcome, which an entry another
// session made doesn't stand for. A bound candidate the session may use is
// bound to that session, since only it holds the handle.
func (c *Cache) selectLookupCandidateForSessionLocked(sessionID string, candidates *set.TreeSet[*sharedResult]) *sharedResult {
	if candidates == nil {
		return nil
	}
	var first *sharedResult
	for res := range candidates.Items() {
		if first != nil && res.boundSession == "" {
			continue
		}
		if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, res) || !servableLocked(res) {
			continue
		}
		if res.boundSession != "" {
			return res
		}
		if first == nil {
			first = res
		}
	}
	return first
}

// servableLocked reports whether a lookup may return res: not while a part of
// its value that a demand found no source for is still incomplete. A local
// entry always has its parts or the operation that makes them; this one has
// neither until its call runs again. Requires egraphMu.
func servableLocked(res *sharedResult) bool {
	if len(res.unavailableParts) == 0 {
		return true
	}
	complete := completePartKeysLocked(res)
	for key := range res.unavailableParts {
		if !slices.Contains(complete, key) {
			return false
		}
	}
	return true
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
	c.appendDigestResultsLocked(candidates, recipeDigest, nowUnix, &match.expired)
	if !candidates.Empty() {
		match.candidates = candidates
		match.hitRecipeDigest = true
		match.route = CacheHitRouteRecipe
		return match
	}
	for _, extra := range extraDigests {
		c.appendDigestResultsLocked(candidates, extra.Digest, nowUnix, &match.expired)
	}
	if !candidates.Empty() {
		match.candidates = candidates
		match.route = CacheHitRouteDigest
	}
	return match
}

// lookupMatchForCallLocked only reads the e-graph: the caller holds egraphMu
// for reading or writing.
// termInputClassesLocked resolves a term's input digests to their classes'
// roots, as the structural lookup matches them. missing is the index of the
// first input no class has, with the classes before it resolved, or -1 when
// every input resolves. Requires egraphMu.
func (c *Cache) termInputClassesLocked(inputDigests []digest.Digest) (ids []eqClassID, missing int) {
	ids = make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		classID, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			return ids, i
		}
		root := c.eqClassRootLocked(classID)
		if root == 0 {
			return ids, i
		}
		ids[i] = root
	}
	return ids, -1
}

func (c *Cache) lookupMatchForCallLocked(
	frame *ResultCall,
	recipeDigest digest.Digest,
	selfDigest digest.Digest,
	inputDigests []digest.Digest,
	nowUnix int64,
) lookupMatch {
	match := lookupMatch{
		primaryLookupPossible: true,
		missingInputIndex:     -1,
	}
	if frame == nil {
		return match
	}

	match = c.lookupMatchForDigestsLocked(recipeDigest, frame.ExtraDigests, nowUnix)
	if match.candidates != nil && !match.candidates.Empty() {
		return match
	}

	match.selfDigest = selfDigest
	match.inputDigests = inputDigests
	match.inputEqIDs, match.missingInputIndex = c.termInputClassesLocked(inputDigests)
	match.primaryLookupPossible = match.missingInputIndex < 0
	if match.primaryLookupPossible {
		match.termDigest = calcEgraphTermDigest(selfDigest, match.inputEqIDs)
		termSet := c.egraphTermsByTermDigest.Members(match.termDigest)
		match.termSetSize = termSet.Size()
		candidates := newSharedResultSet()
		c.appendTermSetResultsLocked(candidates, termSet, nowUnix, &match.expired)
		if !candidates.Empty() {
			match.candidates = candidates
			match.route = CacheHitRouteStructural
		}
	}
	return match
}

// freshInputsLocked returns the indices of the input classes that are the
// input of no term: no term can match while one of them is, so they are the
// inputs new to the cache. nil when there are none. Requires egraphMu.
func (c *Cache) freshInputsLocked(inputEqIDs []eqClassID) []int {
	var fresh []int
	for i, id := range inputEqIDs {
		if c.inputEqClassToTerms.Size(id) == 0 {
			fresh = append(fresh, i)
		}
	}
	return fresh
}

func (c *Cache) indexResultDigestsLocked(res *sharedResult, requestFrame, responseFrame *ResultCall) error {
	if res == nil {
		return nil
	}
	c.initEgraphLocked()

	// The frames' recipe digests are derived before egraphMu is taken:
	// deriving one resolves its inputs under egraphMu.
	indexFrame := func(frame *ResultCall) error {
		if frame == nil {
			return nil
		}
		dig, derived, err := frame.derivedRecipeDigest()
		if !derived {
			return fmt.Errorf("frame %s indexed before its recipe digest was derived", frame.Field)
		}
		if err != nil {
			return err
		}
		c.addFramePostingsLocked(res.id, dig, frame.ExtraDigests)
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

// addResultDigestPostingLocked requires egraphMu and an e-graph initialized by
// initEgraphLocked.
func (c *Cache) addResultDigestPostingLocked(resID sharedResultID, dig string) {
	if resID == 0 || dig == "" {
		return
	}
	if c.egraphResultsByDigest.Insert(dig, resID) {
		c.resultIndexedDigests[resID] = append(c.resultIndexedDigests[resID], dig)
	}
}

// markResultBroadlyIndexedLocked requires egraphMu and an e-graph initialized
// by initEgraphLocked.
func (c *Cache) markResultBroadlyIndexedLocked(resID sharedResultID) {
	if resID == 0 {
		return
	}
	c.broadlyIndexedResults[resID] = struct{}{}
}

func (c *Cache) removeResultDigestPostingLocked(resID sharedResultID, dig string) {
	if resID == 0 || dig == "" {
		return
	}
	c.egraphResultsByDigest.Remove(dig, resID)
}

func (c *Cache) removeResultDigestsLocked(resID sharedResultID, outputEqClasses map[eqClassID]struct{}) {
	if resID == 0 {
		return
	}

	for _, dig := range c.resultIndexedDigests[resID] {
		c.removeResultDigestPostingLocked(resID, dig)
	}
	if groups := c.restoredDigestGroups[resID]; len(groups) != 0 {
		c.removeImportedPostingGroupsLocked(resID, groups)
	} else if _, broad := c.broadlyIndexedResults[resID]; broad {
		for outputEqID := range outputEqClasses {
			outputEqID = c.findEqClassLocked(outputEqID)
			if outputEqID == 0 {
				continue
			}
			for dig := range c.eqClassToDigests[outputEqID].All() {
				c.removeResultDigestPostingLocked(resID, dig)
			}
		}
	}
	delete(c.resultIndexedDigests, resID)
	delete(c.restoredDigestGroups, resID)
	delete(c.broadlyIndexedResults, resID)
}

// lookupCacheForRequestLocked checks if the given call ID has an equivalent result in the cache.
// It first attempts direct digest lookup using the request recipe/extra digests. If that misses,
// it falls back to the canonical term lookup using (self, input eq-classes).
//
// With readOnly set the caller holds only the egraphMu read lock. A miss and a
// plain recipe-digest hit change nothing in the e-graph and are answered as
// usual; any other hit would teach the e-graph the request's identity, so it
// changes nothing and reports needsWrite, and the caller retries the lookup
// under the write lock. Without readOnly the caller holds the write lock.
func (c *Cache) lookupCacheForRequestLocked(
	ctx context.Context,
	sessionID string,
	req *CallRequest,
	requestDigest digest.Digest,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []ResultCallStructuralInputRef,
	readOnly bool,
) (_ AnyResult, hit bool, persistedEdgeExpiresAtUnix int64, needsWrite bool, _ error) {
	if req == nil || req.ResultCall == nil {
		return nil, false, 0, false, nil
	}
	now := time.Now()
	nowUnix := now.Unix()
	persistedEdgeExpiresAtUnix = candidateSharedResultExpiryUnix(nowUnix, req.TTL)
	var match lookupMatch
	if req.ListItem {
		match = c.lookupMatchForListItemLocked(req, nowUnix)
	} else {
		match = c.lookupMatchForCallLocked(req.ResultCall, requestDigest, requestSelf, requestInputs, nowUnix)
	}
	c.traceLookupAttempt(ctx, requestDigest.String(), match.selfDigest.String(), match.inputDigests, req.IsPersistable)
	hitRes := c.selectLookupCandidateForSessionLocked(sessionID, match.candidates)

	// Fill the request's cache-evidence carrier (when armed) with the facts
	// that exist only at this decision point; plain field writes of values
	// already in hand, changing nothing about the lookup itself.
	if ev := req.CacheEvidence; ev != nil {
		ev.LookupAtUnixNano = now.UnixNano()
		if hitRes != nil {
			ev.HitRoute = match.route
			ev.HitRemote = hitRes.imported
			if origin := hitRes.merged; origin != nil {
				ev.HitMergedAtUnixNano = origin.committedAt
				ev.HitMergedRoute, ev.HitMerge, ev.HitMergedFile = origin.route, origin.number, origin.file
				ev.HitFirstUse = hitRes.holdsUnpreparedPayload()
			}
		} else {
			ev.MissSawExpired = match.expired.any
			ev.MissExpiredRemote = match.expired.remote
			// Expiry is applied during accumulation, so each candidate a
			// miss had failed the session-resource filter, or was passed by
			// for a part no source could supply.
			if match.candidates != nil {
				for res := range match.candidates.Items() {
					if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, res) {
						ev.MissIncompatibleCandidates = true
						ev.MissIncompatibleRemote = ev.MissIncompatibleRemote || res.imported
					} else {
						ev.MissUnavailableParts = true
					}
				}
			}
			if !match.primaryLookupPossible {
				ev.MissUnknownInputIndex = match.missingInputIndex
			} else {
				ev.MissFreshInputs = c.freshInputsLocked(match.inputEqIDs)
			}
		}
	}

	if hitRes == nil {
		c.traceLookupMissNoMatch(ctx, requestDigest.String(), match.primaryLookupPossible, match.missingInputIndex, match.termDigest, match.termSetSize)
		return nil, false, 0, false, nil
	}

	// fast-path: if we got a very simple recipe-digest hit we can skip trying to teach the egraph anything new
	if requestDigest != "" && len(req.ResultCall.ExtraDigests) == 0 && req.TTL == 0 && !req.IsPersistable && match.hitRecipeDigest {
		touchSharedResultLastUsed(hitRes, now.UnixNano())
		retRes := Result[Typed]{
			shared:   hitRes,
			hitCache: true,
		}
		c.traceLookupHit(ctx, requestDigest.String(), hitRes, match.termDigest)
		return retRes, true, 0, false, nil
	}
	if readOnly {
		// A repeat hit usually finds its identity already taught: then the
		// write path below would change nothing, and the hit is answered
		// under the read lock too.
		if !c.hitTeachesNothingLocked(hitRes, req, requestDigest, requestSelf, requestInputs, requestInputRefs, persistedEdgeExpiresAtUnix) {
			return nil, false, 0, true, nil
		}
		touchSharedResultLastUsed(hitRes, now.UnixNano())
		retRes := Result[Typed]{
			shared:   hitRes,
			hitCache: true,
		}
		c.traceLookupHit(ctx, requestDigest.String(), hitRes, match.termDigest)
		return retRes, true, persistedEdgeExpiresAtUnix, false, nil
	}

	// We have a cache hit. Teach this request identity onto the existing shared
	// result so any raw ID we hand back is itself resolvable by the cache later.
	res := hitRes
	// A TTL-bearing call can alias an existing result on lookup; apply the same
	// conservative expiry merge policy here so TTL remains effective on hits.
	res.expiresAtUnix = mergeSharedResultExpiryUnix(
		res.expiresAtUnix,
		persistedEdgeExpiresAtUnix,
	)
	c.markEntryClassLocked(res.id, StampEdgeExpiry)
	touchSharedResultLastUsed(res, now.UnixNano())
	if err := c.teachResultIdentityLocked(ctx, res, req.ResultCall, requestDigest, requestSelf, requestInputs, requestInputRefs); err != nil {
		return nil, false, 0, false, err
	}
	retRes := Result[Typed]{
		shared:   res,
		hitCache: true,
	}
	c.traceLookupHit(ctx, requestDigest.String(), res, match.termDigest)
	return retRes, true, persistedEdgeExpiresAtUnix, false, nil
}

// hitTeachesNothingLocked reports whether the write path of a hit (the
// expiry merge and teachResultIdentityLocked) would leave every structure
// as it is: the request's digests already in the result's single output
// class, the result already associated with the request's term, every term
// with that digest already pointing at that class, and the result already
// posted under its recipe and extra digests. It only reads, finding roots
// without path compression, and answers false whenever any step would
// write. The caller holds egraphMu, for reading or writing; the request's
// recipe digest is derived before the lookup, so deriving it here reads the
// memoized value without taking the lock again.
//
//nolint:gocyclo // One read-only check per write the hit's write path could make; splitting them would hide that they mirror it.
func (c *Cache) hitTeachesNothingLocked(
	res *sharedResult,
	req *CallRequest,
	requestDigest digest.Digest,
	requestSelf digest.Digest,
	requestInputs []digest.Digest,
	requestInputRefs []ResultCallStructuralInputRef,
	expiresAtUnix int64,
) bool {
	frame := req.ResultCall
	if res == nil || res.id == 0 || frame == nil {
		return false
	}
	if mergeSharedResultExpiryUnix(res.expiresAtUnix, expiresAtUnix) != res.expiresAtUnix {
		return false
	}
	// An error here fails the write path too; leave it to report it.
	if _, err := c.inputProvenanceForRefs(requestInputRefs); err != nil {
		return false
	}
	indexDigest, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return false
	}
	roots := c.outputEqClassRootsLocked(res.id)
	if len(roots) != 1 {
		return false
	}
	var outputRoot eqClassID
	for root := range roots {
		outputRoot = root
	}
	inOutputClass := func(dig string) bool {
		id, ok := c.egraphDigestToClass[dig]
		return ok && c.eqClassRootLocked(id) == outputRoot
	}
	if requestDigest != "" && !inOutputClass(requestDigest.String()) {
		return false
	}
	for _, extra := range frame.ExtraDigests {
		if extra.Digest != "" && !inOutputClass(extra.Digest.String()) {
			return false
		}
	}
	inputEqIDs := make([]eqClassID, len(requestInputs))
	for i, inDig := range requestInputs {
		if inDig == "" {
			continue
		}
		id, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			return false
		}
		inputEqIDs[i] = c.eqClassRootLocked(id)
	}
	termDigest := calcEgraphTermDigest(requestSelf, inputEqIDs)
	if c.termForResultByDigestLocked(res.id, termDigest) == nil {
		return false
	}
	// mergeOutputsForTermDigestLocked stores the root on every term of the
	// digest, so a term still holding a non-root class would be written.
	{
		for termID := range c.egraphTermsByTermDigest.Items(termDigest) {
			if term := c.egraphTerms[termID]; term != nil && term.outputEqID != outputRoot {
				return false
			}
		}
	}
	postedUnder := func(dig string) bool {
		if dig == "" {
			return true
		}
		return c.egraphResultsByDigest.Contains(dig, res.id)
	}
	if !postedUnder(indexDigest.String()) {
		return false
	}
	for _, extra := range frame.ExtraDigests {
		if !postedUnder(extra.Digest.String()) {
			return false
		}
	}
	for _, extra := range frame.ExtraDigests {
		if extra.Digest == "" {
			continue
		}
		for termID := range c.resultTerms.All(res.id) {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			root := c.eqClassRootLocked(term.outputEqID)
			if root == 0 {
				continue
			}
			if !slices.Contains(c.eqClassExtraDigests[root], extra) {
				return false
			}
		}
	}
	return true
}

// claimLookupHitLocked records the session's ownership of a selected hit and
// captures its requirement generation. The caller holds egraphMu for reading
// or writing, in the same critical section that selected the hit.
func (c *Cache) claimLookupHitLocked(ctx context.Context, sessionID string, retRes AnyResult) (*sharedResult, int, uint64, error) {
	hitShared := retRes.cacheSharedResult()
	if hitShared == nil || hitShared.id == 0 {
		return nil, 0, 0, fmt.Errorf("lookup cache for request: hit missing shared result ID")
	}
	_, trackedCount, err := c.acquireSessionResultLocked(ctx, sessionID, hitShared)
	if err != nil {
		return nil, 0, 0, err
	}
	// Capture the requirement generation inside the same critical section
	// that ran the selection-time subset check. Captured after the unlock, a
	// concurrent growth's bump could already be reflected in the captured
	// value, the serve-time comparison would see equality, and the stale
	// serve would go through.
	return hitShared, trackedCount, hitShared.requiredSessionResourcesGen.Load(), nil
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
	// A result the session bound to itself is published under the call
	// holding the binding (boundSessionInputName), and is the session's own
	// outcome of the call: a session that has bound one for a call of this
	// field looks the call up that way first, before another session's
	// unbound result of it.
	if req != nil && req.ResultCall != nil {
		if c.sessionMayHaveBound(sessionID, req.Field) {
			bound := req.withBoundSession(c.sessionBoundHandle(sessionID))
			// The same invocation looks again: its evidence is the one it
			// records.
			bound.CacheEvidence = req.CacheEvidence
			boundDigest, err := bound.deriveRecipeDigest(c)
			if err != nil {
				return nil, false, fmt.Errorf("derive bound request digest: %w", err)
			}
			boundSelf, boundInputRefs, err := bound.selfDigestAndInputRefs(c)
			if err != nil {
				return nil, false, fmt.Errorf("derive bound request term digests: %w", err)
			}
			boundInputs := make([]digest.Digest, 0, len(boundInputRefs))
			for _, ref := range boundInputRefs {
				dig, err := ref.inputDigest(c)
				if err != nil {
					return nil, false, fmt.Errorf("derive bound request term input digest: %w", err)
				}
				boundInputs = append(boundInputs, dig)
			}
			res, hit, err := c.lookupCacheForRequestDigests(ctx, sessionID, resolver, bound, boundDigest, boundSelf, boundInputs, boundInputRefs)
			if err != nil || hit {
				req.servedAsSessions = hit
				return res, hit, err
			}
		}
	}
	return c.lookupCacheForRequestDigests(ctx, sessionID, resolver, req, requestDigest, requestSelf, requestInputs, requestInputRefs)
}

// sessionHasBound reports whether sessionID has bound a result to itself,
// so that it holds its binding's handle (boundSessionHandle). It is asked two
// or three times on every call of every session, so it reads the flag on the
// session's lifecycle record, which every session operation already reaches
// without the session lock, and a session that has bound nothing takes no
// lock for it.
func (c *Cache) sessionHasBound(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	state, ok := c.sessionLifecycles.Load(sessionID)
	return ok && state.(*cacheSessionLifecycle).bound.Load()
}

// noteBoundCall records that sessionID has published a result under its own
// binding for a call of field.
func (c *Cache) noteBoundCall(sessionID, field string) {
	if state, ok := c.sessionLifecycles.Load(sessionID); ok {
		state.(*cacheSessionLifecycle).boundFields.Store(field, struct{}{})
	}
}

// sessionMayHaveBound reports whether sessionID may hold a result bound to
// itself for a call of field: it has published one for a call of that field
// (noteBoundCall). A bound result is found only by a call of its own field,
// whose binding is part of the call, so a session's lookups of every other
// field skip looking for one, and a session that has bound nothing takes no
// lock for it.
func (c *Cache) sessionMayHaveBound(sessionID, field string) bool {
	if sessionID == "" {
		return false
	}
	state, ok := c.sessionLifecycles.Load(sessionID)
	if !ok {
		return false
	}
	lifecycle := state.(*cacheSessionLifecycle)
	if !lifecycle.bound.Load() {
		return false
	}
	_, ok = lifecycle.boundFields.Load(field)
	return ok
}

func (c *Cache) lookupCacheForRequestDigests(
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

	// Most lookups are a miss or a plain recipe-digest hit. Neither changes
	// the e-graph, and the hit's session claim changes only state with its
	// own synchronization, so they run under the read lock and concurrent
	// lookups do not queue behind each other.
	readHold := c.lookupReadLockGraph(ctx)
	retRes, hit, persistedEdgeExpiresAtUnix, needsWrite, err := c.lookupCacheForRequestLocked(ctx, sessionID, req, requestDigest, requestSelf, requestInputs, requestInputRefs, true)
	if err != nil || (!hit && !needsWrite) {
		readHold.Unlock()
		return retRes, hit, err
	}
	var (
		hitShared              *sharedResult
		trackedCount           int
		requiredGenAtSelection uint64
	)
	if hit {
		hitShared, trackedCount, requiredGenAtSelection, err = c.claimLookupHitLocked(ctx, sessionID, retRes)
		readHold.Unlock()
	} else {
		// The hit teaches the e-graph: look it up again under the write lock.
		readHold.Unlock()
		c.lookupLockGraph(ctx)
		retRes, hit, persistedEdgeExpiresAtUnix, _, err = c.lookupCacheForRequestLocked(ctx, sessionID, req, requestDigest, requestSelf, requestInputs, requestInputRefs, false)
		if err == nil && hit {
			hitShared, trackedCount, requiredGenAtSelection, err = c.claimLookupHitLocked(ctx, sessionID, retRes)
		}
		c.egraphMu.Unlock()
		if err == nil && !hit {
			return retRes, false, nil
		}
	}
	if err != nil {
		return nil, false, err
	}

	// From here on the hit's own work gathers what it waits for and what
	// its load decodes, for its span (CacheDecision.HitLoad).
	ev := req.CacheEvidence
	if ev != nil {
		ev.hitLoad = &hitLoad{}
		ctx = withHitLoad(ctx, ev.hitLoad)
	}
	loadedHit, err := c.ensurePersistedHitValueLoaded(ctx, resolver, retRes)
	if ev != nil && err == nil {
		ev.HitLoadedAtUnixNano = time.Now().UnixNano()
	}
	if err != nil {
		if errors.Is(err, errAttachRefusedByProducerRelease) {
			// The hit's attachment failed only because the producing
			// session was released before its attachment-time claim ran.
			// This reader is innocent and a fresh execution would succeed,
			// so fall through to the singleflight instead of surfacing the
			// producer's release as this caller's failure. The recorded
			// session edge stays until session release, exactly as the
			// requirement re-validation's miss conversion below keeps it.
			if ev != nil {
				ev.MissProducerReleased = true
			}
			return nil, false, nil
		}
		if errors.Is(err, errMergedPayloadRefused) {
			// The hit's record, merged from the Cloud, failed its check at
			// its first use, or one it depends on did: they are taken back
			// (takeBackRefused), and the call runs instead, as it would
			// have had the record never arrived.
			c.takeBackRefused(ctx, hitShared, err)
			return nil, false, nil
		}
		return nil, false, err
	}
	if c.testBeforeServeRequirementRecheck != nil {
		c.testBeforeServeRequirementRecheck(hitShared)
	}
	if !c.sessionStillSatisfiesResourceRequirements(ctx, sessionID, hitShared, requiredGenAtSelection) {
		// The required set grew after this hit was selected (an attached or
		// late explicit dep carried requirements), so serving would hand
		// the session a result it no longer satisfies. Fall through to the
		// singleflight instead. The recorded session edge stays until
		// session release; over-retaining one result is cheaper than a
		// dedicated decrement-and-collect path here.
		if ev != nil {
			ev.MissRequirementsGrew = true
		}
		return nil, false, nil
	}
	if req.IsPersistable {
		// Only persistable hits pay this second graph-lock acquisition, and
		// only for writing when the upsert would change the edge. Keep the
		// expiry captured at hit selection even if the barrier wait took long
		// enough to cross a wall-clock second. A value its call declined to
		// persist stays unpersisted.
		if ev != nil {
			ev.Persisted = !hitShared.persistDeclined
		}
		readHold = c.lookupReadLockGraph(ctx)
		unchanged := hitShared.persistDeclined || c.persistedEdgeUpsertUnchangedLocked(hitShared, persistedEdgeExpiresAtUnix)
		readHold.Unlock()
		if !unchanged {
			c.lookupLockGraph(ctx)
			if !hitShared.persistDeclined {
				c.upsertPersistedEdgeLocked(ctx, hitShared, persistedEdgeExpiresAtUnix, false)
			}
			c.egraphMu.Unlock()
		}
	}

	if c.traceEnabled() {
		c.traceSessionResultTracked(ctx, sessionID, loadedHit, true, trackedCount)
	}
	return loadedHit, true, nil
}

func (c *Cache) TeachCallEquivalentToResult(ctx context.Context, sessionID string, frame *ResultCall, res AnyResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sessionID != "" && c.sessionLifecycle(sessionID).lifecycle.Load()&cacheSessionReleasedBit != 0 {
		return fmt.Errorf("%w: %q", ErrCacheSessionReleased, sessionID)
	}
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

	c.lockGraphFor(ctx, workTeach)
	defer c.egraphMu.Unlock()
	// Digest derivation above is outside egraphMu, so release can collect
	// the result in that gap. Numeric result IDs are engine-lifetime unique,
	// so registration under the ID means it is still this exact result.
	if _, found := c.resultsByID[shared.id]; !found {
		return fmt.Errorf("teach call equivalence: result %d was already collected", shared.id)
	}
	return c.teachResultIdentityLocked(ctx, shared, frame, requestDigest, requestSelf, requestInputs, requestInputRefs)
}

func (c *Cache) TeachContentDigest(ctx context.Context, res AnyResult, contentDigest digest.Digest, additionalLabels ...string) error {
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
	_, err := c.teachContentDigest(ctx, shared, contentDigest, additionalLabels, false)
	return err
}

// teachContentDigest is TeachContentDigest on an attached result. With
// lacking, a frame whose content digest is already contentDigest is left as
// it is, and reports no rewrite: the body worker gives a Cloud entry's frame
// the content digest a span taught it only when the frame lacks it
// (applyTaughtContent). It reports whether it replaced the frame.
func (c *Cache) teachContentDigest(ctx context.Context, shared *sharedResult, contentDigest digest.Digest, additionalLabels []string, lacking bool) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		// A blob-backed cache's entry can hold its frame in its stored
		// body: it is decoded here, and replaced with the body below.
		base := entryFrame{call: shared.loadResultCall()}
		if base.call == nil {
			readHold := c.readGraphAs(workTeach)
			base = shared.entryFrameLocked()
			readHold.Unlock()
		}
		baseFrame, err := base.decode()
		if err != nil {
			return false, fmt.Errorf("teach content digest: %w", err)
		}
		if baseFrame == nil {
			return false, fmt.Errorf("teach content digest: result %d has no call frame", shared.id)
		}
		oldContentDigest := baseFrame.ContentDigest()
		if lacking && oldContentDigest == contentDigest {
			// The frame carries it: nothing to rewrite. Its content digest is
			// known now, as a decoded frame's is.
			shared.resultCallMu.Lock()
			if shared.resultCall == nil || shared.resultCall == base.call {
				shared.frameContent, shared.frameContentKnown = oldContentDigest, true
			}
			shared.resultCallMu.Unlock()
			return false, nil
		}
		frame := frameWithContentDigest(baseFrame, contentDigest, additionalLabels)

		requestDigest, err := frame.deriveRecipeDigest(c)
		if err != nil {
			return false, fmt.Errorf("teach content digest: derive request digest: %w", err)
		}
		requestSelf, requestInputRefs, err := frame.selfDigestAndInputRefs(c)
		if err != nil {
			return false, fmt.Errorf("teach content digest: derive request term digests: %w", err)
		}
		requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
		for _, ref := range requestInputRefs {
			dig, err := ref.inputDigest(c)
			if err != nil {
				return false, fmt.Errorf("teach content digest: derive request term input digest: %w", err)
			}
			requestInputs = append(requestInputs, dig)
		}
		// A blob-backed cache's stored record takes the new frame with its
		// parts worked out for it, here, outside the lock: a body is
		// reframed, whether or not it holds the frame, so that its
		// references are the frame's too; a record still whole is mapped
		// with the frame. The record read here is checked under the lock.
		var (
			env   *PersistedResultEnvelope
			was   *storedBody
			body  *storedBody
			parts *partFacts
		)
		if c.blobBacked {
			readHold := c.readGraphAs(workTeach)
			env, was = shared.persistedEnvelope, shared.stored
			readHold.Unlock()
			switch {
			case was != nil:
				if body, parts, err = was.reframe(c, shared.id, frame); err != nil {
					return false, fmt.Errorf("teach content digest: %w", err)
				}
			case env != nil:
				parts = recordPartFacts(PersistedRecord{ResultID: uint64(shared.id), Envelope: *env, Call: frame})
			}
		}
		// A blob-backed cache's record is measured with its frame's JSON
		// (storedRecordBytes), as a restore of the saved frame measures it:
		// the new frame changes the record's size by its JSON's growth.
		var grown int64
		if c.blobBacked {
			if grown, err = frameJSONGrowth(base, baseFrame, body, frame); err != nil {
				return false, fmt.Errorf("teach content digest: %w", err)
			}
		}

		c.lockGraphFor(ctx, workTeach)
		// Numeric result IDs are engine-lifetime unique, so a registered ID
		// still names the caller's result across retries.
		if _, found := c.resultsByID[shared.id]; !found {
			c.egraphMu.Unlock()
			return false, fmt.Errorf("teach content digest: result %d was already collected", shared.id)
		}
		cur := shared.entryFrameLocked()
		if !cur.ok() {
			c.egraphMu.Unlock()
			return false, fmt.Errorf("teach content digest: result %d has no call frame", shared.id)
		}
		if cur != base || c.blobBacked && (shared.persistedEnvelope != env || shared.stored != was) {
			c.egraphMu.Unlock()
			continue
		}
		c.traceTeachContentDigest(ctx, shared, oldContentDigest.String(), contentDigest.String(), requestDigest.String(), requestSelf.String(), requestInputs, frame)
		if err := c.teachResultIdentityLocked(ctx, shared, frame, requestDigest, requestSelf, requestInputs, requestInputRefs); err != nil {
			c.egraphMu.Unlock()
			return false, err
		}
		shared.storeResultCall(frame)
		if body != nil {
			// The new frame goes into a new body, held in it if it can be. The
			// entry, whose record a body held, holds its light envelope.
			c.installStoredBodyLocked(shared, shared.persistedEnvelope, shared.persistedEnvelope, body)
		}
		if parts != nil {
			shared.payloadMu.Lock()
			shared.partFacts = parts
			shared.payloadMu.Unlock()
		}
		if shared.storedRecordBytes != 0 {
			c.setStoredRecordBytesLocked(shared, shared.storedRecordBytes+grown)
		}
		// A stored bundle of the entry carries the new frame, and those of its
		// class mates whatever the identity taught joined; this write hands
		// back no marks.
		c.stampEntryLocked(shared, StampFrame)
		c.stampPendingLocked()
		c.traceResultCallFrameUpdated(ctx, shared, "teach_content_digest", baseFrame, frame)
		c.egraphMu.Unlock()
		return true, nil
	}
}

// frameWithContentDigest returns a fork of baseFrame whose content digest is
// contentDigest, with each additional label for it: a content digest it
// replaces still names the result, so it stays on the frame as
// replaced-content and travels with the result to other engines.
func frameWithContentDigest(baseFrame *ResultCall, contentDigest digest.Digest, additionalLabels []string) *ResultCall {
	frame := baseFrame.fork()

	replaced := false
	var replacedDigest digest.Digest
	for i, extra := range frame.ExtraDigests {
		if extra.Label != call.ExtraDigestLabelContent {
			continue
		}
		if extra.Digest != contentDigest {
			replacedDigest = extra.Digest
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
	if replacedDigest != "" {
		kept := call.ExtraDigest{Digest: replacedDigest, Label: call.ExtraDigestLabelReplacedContent}
		if !slices.Contains(frame.ExtraDigests, kept) {
			frame.ExtraDigests = append(frame.ExtraDigests, kept)
		}
	}

	for _, label := range additionalLabels {
		extra := call.ExtraDigest{Digest: contentDigest, Label: label}
		if !slices.Contains(frame.ExtraDigests, extra) {
			frame.ExtraDigests = append(frame.ExtraDigests, extra)
		}
	}
	return frame
}

// frameJSONGrowth is how much longer the JSON of frame, the frame replacing
// baseFrame, is than baseFrame's: from the bodies' kept lengths when the
// frames are held in them, and encoded otherwise. It reads no lock.
func frameJSONGrowth(base entryFrame, baseFrame *ResultCall, body *storedBody, frame *ResultCall) (int64, error) {
	length := func(held *storedBody, f *ResultCall) (int64, error) {
		if held.holdsFrame() {
			return int64(held.frameBytes), nil
		}
		raw, err := json.Marshal(f)
		if err != nil {
			return 0, fmt.Errorf("measure call frame: %w", err)
		}
		return int64(len(raw)), nil
	}
	was, err := length(base.body, baseFrame)
	if err != nil {
		return 0, err
	}
	now, err := length(body, frame)
	if err != nil {
		return 0, err
	}
	return now - was, nil
}

// resultIDForCall finds the attached result for frame that sessionID would be
// served for it, as a lookup of the call by the session would choose it: a
// call whose key doesn't name its session can have a result for each
// session, built on resources only that session has bound, and what is made
// from a result requires what it requires. A result the session bound to
// itself is found under the call holding the binding first, as
// lookupCacheForRequest finds it.
func (c *Cache) resultIDForCall(ctx context.Context, sessionID string, frame *ResultCall) (sharedResultID, error) {
	if frame == nil {
		return 0, fmt.Errorf("resolve result ID for call: nil call")
	}
	if c.sessionMayHaveBound(sessionID, frame.Field) {
		bound := frame.withBoundSessions([]SessionResourceHandle{c.sessionBoundHandle(sessionID)})
		if id, _, err := c.sessionResultIDForCall(ctx, sessionID, bound); err != nil || id != 0 {
			return id, err
		}
	}
	id, requestDigest, err := c.sessionResultIDForCall(ctx, sessionID, frame)
	if err != nil || id != 0 {
		return id, err
	}
	return 0, fmt.Errorf("resolve result ID for call: no attached result for %s", requestDigest)
}

// sessionResultIDForCall is resultIDForCall for frame as given: the number of
// the candidate selectLookupCandidateForSessionLocked chooses for sessionID,
// or 0 when there is none, with frame's recipe digest. The lookup only reads
// the e-graph, so it holds egraphMu for reading, as a cache-hit lookup does.
func (c *Cache) sessionResultIDForCall(ctx context.Context, sessionID string, frame *ResultCall) (sharedResultID, digest.Digest, error) {
	requestDigest, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return 0, "", fmt.Errorf("resolve result ID for call: derive request digest: %w", err)
	}
	requestSelf, requestInputRefs, err := frame.selfDigestAndInputRefs(c)
	if err != nil {
		return 0, "", fmt.Errorf("resolve result ID for call: derive request term digests: %w", err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return 0, "", fmt.Errorf("resolve result ID for call: derive request term input digest: %w", err)
		}
		requestInputs = append(requestInputs, dig)
	}

	read := c.readGraph(ctx, workLookup)
	defer read.Unlock()
	match := c.lookupMatchForCallLocked(frame, requestDigest, requestSelf, requestInputs, time.Now().Unix())
	if res := c.selectLookupCandidateForSessionLocked(sessionID, match.candidates); res != nil {
		return res.id, requestDigest, nil
	}
	return 0, requestDigest, nil
}

// termForResultByDigestLocked returns a live term with termDigest that the
// result is associated with. It walks the terms with that digest, not the
// result's terms: a long-lived result gathers a term for every recipe that
// reached it, such as one per session for a session-scoped call.
func (c *Cache) termForResultByDigestLocked(resID sharedResultID, termDigest string) *egraphTerm {
	if c.resultTerms.Size(resID) == 0 {
		return nil
	}
	for termID := range c.egraphTermsByTermDigest.Items(termDigest) {
		if !c.resultTerms.Contains(resID, termID) {
			continue
		}
		if term := c.egraphTerms[termID]; term != nil {
			return term
		}
	}
	return nil
}

// addResultExtraDigestsLocked records extra digests as facts of each of the
// result's output eq-classes, which hold the outputs of all its terms.
func (c *Cache) addResultExtraDigestsLocked(resID sharedResultID, frames ...*ResultCall) {
	for outputEqID := range c.outputEqClassesForResultLocked(resID) {
		c.noteSavedClassLocked(outputEqID)
		extras := c.eqClassExtraDigests[outputEqID]
		for _, frame := range frames {
			if frame == nil {
				continue
			}
			for _, extra := range frame.ExtraDigests {
				if extra.Digest == "" {
					continue
				}
				extras = withExtraDigests(extras, extra)
			}
		}
		if len(extras) > 0 {
			c.eqClassExtraDigests[outputEqID] = extras
		}
	}
}

// withExtraDigests returns extras with each of adds it lacks appended. A
// class's extra digests are a short list, nearly always of one content
// digest, where a Go map would take a header and a group of eight slots.
func withExtraDigests(extras []call.ExtraDigest, adds ...call.ExtraDigest) []call.ExtraDigest {
	for _, add := range adds {
		if !slices.Contains(extras, add) {
			extras = append(extras, add)
		}
	}
	return extras
}

func (c *Cache) outputEqClassesForResultLocked(resID sharedResultID) map[eqClassID]struct{} {
	classes := c.resultOutputEqClasses.Members(resID)
	if classes.Size() == 0 {
		return nil
	}
	out := make(map[eqClassID]struct{}, classes.Size())
	for eqID := range classes.All() {
		root := c.findEqClassLocked(eqID)
		if root == 0 {
			continue
		}
		out[root] = struct{}{}
	}
	return out
}

// addResultOutputEqClassLocked requires egraphMu and an e-graph initialized by
// initEgraphLocked.
func (c *Cache) addResultOutputEqClassLocked(resID sharedResultID, outputEqID eqClassID) {
	if resID == 0 || outputEqID == 0 {
		return
	}
	c.noteSavedEntryLocked(resID)
	outputEqID = c.findEqClassLocked(outputEqID)
	if outputEqID == 0 {
		return
	}

	if c.resultOutputEqClasses.Insert(resID, outputEqID) {
		c.useEqClassLocked(outputEqID)
	}
	c.outputEqClassResults.Insert(outputEqID, resID)
}

// removeResultOutputEqClassesLocked requires egraphMu and an e-graph initialized
// by initEgraphLocked.
func (c *Cache) removeResultOutputEqClassesLocked(resID sharedResultID) {
	if resID == 0 {
		return
	}
	c.markLeavingLocked(resID)
	c.noteSavedEntryLocked(resID)
	for outputEqID := range c.resultOutputEqClasses.All(resID) {
		outputEqID = c.findEqClassLocked(outputEqID)
		if outputEqID == 0 {
			continue
		}
		c.unuseEqClassLocked(outputEqID)
		c.outputEqClassResults.Remove(outputEqID, resID)
	}
	c.resultOutputEqClasses.Delete(resID)
}

func (c *Cache) termIDsForResultLocked(resID sharedResultID) map[egraphTermID]struct{} {
	n := c.resultTerms.Size(resID)
	if n == 0 {
		return nil
	}
	out := make(map[egraphTermID]struct{}, n)
	for termID := range c.resultTerms.All(resID) {
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
	set := c.egraphTermsByTermDigest.Members(termDigest)
	if set.Size() == 0 {
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
			c.setTermOutputLocked(term, root)
		}
		return root
	}
	root = c.mergeEqClassesLocked(ctx, mergeIDs...)
	// The merge's repairs may have moved terms between digests: read the
	// digest's terms as they are now.
	for termID := range c.egraphTermsByTermDigest.Items(termDigest) {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		c.setTermOutputLocked(term, root)
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
	c.noteSavedTermLocked(termID)
	if existing, ok := c.termInputProvenance[termID]; ok {
		if !sameInputProvenance(existing, inputProvenance) {
			c.termInputProvenance[termID] = slices.Clone(inputProvenance)
			c.traceResultTermAssocUpdated(ctx, res.id, termID, inputProvenance)
		}
	} else {
		c.termInputProvenance[termID] = slices.Clone(inputProvenance)
	}

	c.addResultOutputEqClassLocked(res.id, outputEqID)

	if !c.termResults.Insert(termID, res.id) {
		return
	}
	c.resultTerms.Insert(res.id, termID)
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
	provenance, err := c.inputProvenanceForRefs(requestInputRefs)
	if err != nil {
		return err
	}
	indexDigest, err := requestFrame.deriveRecipeDigest(c)
	if err != nil {
		return err
	}
	c.applyPreparedResultIdentityLocked(ctx, res, requestFrame, requestDigest, requestSelf, requestInputs, provenance, indexDigest)
	return nil
}

// applyPreparedResultIdentityLocked has no fallible work after its first mutation.
func (c *Cache) applyPreparedResultIdentityLocked(ctx context.Context, res *sharedResult, requestFrame *ResultCall, requestDigest, requestSelf digest.Digest, requestInputs []digest.Digest, inputProvenance []egraphInputProvenanceKind, indexDigest digest.Digest) {
	if res == nil || res.id == 0 || requestFrame == nil {
		return
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
		return
	}

	mergeIDs := make([]eqClassID, 0, len(rootSet))
	for root := range rootSet {
		if root == 0 {
			continue
		}
		mergeIDs = append(mergeIDs, root)
	}
	if len(mergeIDs) == 0 {
		return
	}
	c.traceTeachResultIdentityRootSet(ctx, res, requestDigest.String(), requestSelf.String(), requestInputs, requestFrame, mergeIDs)
	outputEqID := c.mergeEqClassesLocked(ctx, mergeIDs...)
	if outputEqID == 0 {
		return
	}

	inputEqIDs := c.ensureTermInputEqIDsLocked(ctx, requestInputs)
	termDigest := calcEgraphTermDigest(requestSelf, inputEqIDs)
	existingTerm := c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest.Members(termDigest))

	switch {
	case c.termForResultByDigestLocked(res.id, termDigest) != nil:
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
	case existingTerm != nil:
		c.associateResultWithTermLocked(ctx, res, existingTerm.id, inputProvenance)
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
	default:
		mergedOutputEqID := c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)

		termID := c.nextEgraphTermID
		c.nextEgraphTermID++

		newTerm := newEgraphTerm(termID, requestSelf, inputEqIDs, mergedOutputEqID)
		c.egraphTerms[termID] = newTerm
		c.useTermLocked(newTerm)
		c.traceTermCreated(ctx, "runtime", "", newTerm)

		c.egraphTermsByTermDigest.Insert(newTerm.termDigest, termID)
		for _, inEqID := range newTerm.inputEqIDs {
			if inEqID == 0 {
				continue
			}
			c.inputEqClassToTerms.Insert(inEqID, termID)
		}
		c.outputEqClassToTerms.Insert(mergedOutputEqID, termID)

		c.associateResultWithTermLocked(ctx, res, termID, inputProvenance)
	}

	c.addFramePostingsLocked(res.id, indexDigest, requestFrame.ExtraDigests)
	c.addResultExtraDigestsLocked(res.id, requestFrame)
}

// addFramePostingsLocked posts a frame's recipe digest and extra digests for
// the result. Requires egraphMu.
func (c *Cache) addFramePostingsLocked(resID sharedResultID, recipe digest.Digest, extras []call.ExtraDigest) {
	c.addResultDigestPostingLocked(resID, recipe.String())
	for _, extra := range extras {
		c.addResultDigestPostingLocked(resID, extra.Digest.String())
	}
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

	// A result that carries an ID but is no longer registered was collected
	// after its last owner released it: its OnRelease already ran, so its
	// payload may reference released resources. Refuse to resurrect it rather
	// than re-publish a dead payload into the cache.
	if res.id != 0 {
		if _, found := c.resultsByID[res.id]; !found {
			return fmt.Errorf("index result %d: result was already collected", res.id)
		}
	}

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
		res.id = c.takeResultIDs(1)
	}
	c.putResultLocked(res)
	// An entry with a frame keeps it, decoded or held compressed in its
	// stored body: a call that returned it teaches it identity, not a frame.
	if requestFrame != nil && !res.hasCallFrameLocked() {
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
		if existingTerm := c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest.Members(termDigest)); existingTerm != nil {
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
		c.useTermLocked(newTerm)
		c.traceTermCreated(ctx, "runtime", "", newTerm)

		c.egraphTermsByTermDigest.Insert(newTerm.termDigest, termID)
		for _, inEqID := range newTerm.inputEqIDs {
			if inEqID == 0 {
				continue
			}
			c.inputEqClassToTerms.Insert(inEqID, termID)
		}
		c.outputEqClassToTerms.Insert(mergedOutputEqID, termID)

		c.associateResultWithTermLocked(ctx, res, termID, inputProvenance)
	}

	if err := c.indexResultDigestsLocked(res, requestFrame, responseFrame); err != nil {
		return err
	}
	c.addResultExtraDigestsLocked(res.id, requestFrame, responseFrame)

	return nil
}

func (c *Cache) removeResultFromEgraphLocked(ctx context.Context, res *sharedResult) {
	if res == nil {
		return
	}
	c.unindexRecipesLocked(res)
	if len(c.egraphTerms) == 0 && len(c.resultsByID) == 1 && c.resultsByID[res.id] == res {
		// The last entry, with no term left: the reset clears everything it
		// indexed.
		c.deleteResultLocked(res)
		c.maybeResetEgraphLocked()
		return
	}

	affectedOutputEqClasses := c.outputEqClassesForResultLocked(res.id)
	for termID := range c.resultTerms.All(res.id) {
		c.termResults.Remove(termID, res.id)
		c.traceResultTermAssocRemoved(ctx, res.id, termID)
	}
	c.resultTerms.Delete(res.id)
	c.removeResultDigestsLocked(res.id, affectedOutputEqClasses)
	c.removeResultOutputEqClassesLocked(res.id)
	oldFrame := res.loadResultCall()
	if oldFrame == nil && c.traceEnabled() {
		oldFrame, _ = res.entryFrameLocked().decode()
	}
	depCount := len(res.deps)
	res.dropResultCall()
	c.deleteResultLocked(res)
	c.traceResultRemoved(ctx, res, oldFrame, depCount)

	nowUnix := time.Now().Unix()
	for outputEqID := range affectedOutputEqClasses {
		if c.hasUnexpiredResultForOutputEqClassLocked(outputEqID, nowUnix) {
			// still some results in this eq class, nothing to clean up yet
			continue
		}

		// no more results in this eq class, get the list of terms with this output eq class and clean them up
		// since their results were pruned
		termIDs := make([]egraphTermID, 0, c.outputEqClassToTerms.Size(outputEqID))
		for termID := range c.outputEqClassToTerms.All(outputEqID) {
			termIDs = append(termIDs, termID)
		}
		for _, termID := range termIDs {
			term := c.egraphTerms[termID]
			if term == nil {
				continue
			}
			c.egraphTermsByTermDigest.Remove(term.termDigest, termID)
			for _, in := range term.inputEqIDs {
				c.inputEqClassToTerms.Remove(in, termID)
			}
			c.outputEqClassToTerms.Remove(outputEqID, termID)
			c.unuseTermLocked(term)
			delete(c.egraphTerms, termID)
			c.termResults.Delete(termID)
			delete(c.termInputProvenance, termID)
			c.traceTermRemoved(ctx, termID)
		}
		c.outputEqClassToTerms.Delete(outputEqID)
	}
	c.maybeResetEgraphLocked()
}

func (c *Cache) maybeResetEgraphLocked() {
	// An entry can hold classes without terms, such as one known only through
	// holdings, and an entry can outlive its class's last term, such as an
	// expired entry a session still owns: nothing is reset while any entry
	// remains. Nor while a save reads the store: a reset starts class and
	// term numbers again.
	if len(c.egraphTerms) != 0 || len(c.resultsByID) != 0 || c.savingLocked() {
		return
	}

	c.egraphDigestToClass = nil
	c.callInputEntries = nil
	c.egraphParents = nil
	c.egraphRanks = nil
	c.egraphUses = nil
	c.liveEqClasses = 0
	c.eqClassToDigests = nil
	c.eqClassExtraDigests = nil
	c.inputEqClassToTerms = idSetIndex[eqClassID, egraphTermID]{}
	c.outputEqClassToTerms = idSetIndex[eqClassID, egraphTermID]{}
	c.resultOutputEqClasses = idSetIndex[sharedResultID, eqClassID]{}
	c.outputEqClassResults = idSetIndex[eqClassID, sharedResultID]{}
	c.termResults = idSetIndex[egraphTermID, sharedResultID]{}
	c.resultTerms = idSetIndex[sharedResultID, egraphTermID]{}
	c.egraphTerms = nil
	c.termInputProvenance = nil
	c.egraphTermsByTermDigest = idSetIndex[string, egraphTermID]{}
	c.egraphResultsByDigest = digestPostings{}
	c.resultIndexedDigests = nil
	c.broadlyIndexedResults = nil
	c.restoredDigestGroups = nil
	c.resultsByID = nil
	c.resultPayloadBytes.Store(0)
	c.entriesByRecipe = nil
	c.remoteCaches = nil
	c.freeEqClassIDs = nil
	c.endedEqClasses = nil
	if c.changes != nil {
		// The classes this hold's marks walked are gone with the rest.
		c.changes.left = nil
		c.changes.leftNamed = nil
	}
	c.nextEgraphTermID = 0
	// nextSharedResultID deliberately survives the reset: numeric result IDs
	// must be engine-lifetime unique because they outlive this index (session
	// records, dependency edges, persisted rows, results held by in-flight
	// callers). Recycling a number could alias two different results. Class
	// and term IDs never leave the index maps cleared above, so their
	// counters can restart. Import seeds nextSharedResultID past the highest
	// persisted ID at startup for the same reason.
}
