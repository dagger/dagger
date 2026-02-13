package dagql

import (
	"strconv"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

type eqClassID uint64
type egraphTermID uint64

type callTermProto struct {
	selfDigest   digest.Digest
	inputDigests []digest.Digest
}

type egraphTerm struct {
	id egraphTermID

	selfDigest digest.Digest
	inputEqIDs []eqClassID
	outputEqID eqClassID
	termDigest string

	result *sharedResult
}

type eqMergePair struct {
	a eqClassID
	b eqClassID
}

func termProtoForID(id *call.ID) (callTermProto, error) {
	if id == nil {
		return callTermProto{}, nil
	}
	// Module call digests can vary across clients/sessions even when the
	// recipe-equivalent call is otherwise the same. Ignore module metadata for
	// term-shape matching; module identity is still captured by input digests
	// (e.g. custom/content digests added by cache-key config).
	selfDigest, inputDigests, err := id.With(call.WithModule(nil)).SelfDigestAndInputs()
	if err != nil {
		return callTermProto{}, err
	}
	return callTermProto{
		selfDigest:   selfDigest,
		inputDigests: inputDigests,
	}, nil
}

func calcEgraphTermDigest(selfDigest digest.Digest, inputEqIDs []eqClassID) string {
	h := hashutil.NewHasher().
		WithString(selfDigest.String())
	for _, in := range inputEqIDs {
		h = h.WithString(strconv.FormatUint(uint64(in), 10))
	}
	return h.DigestAndClose()
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
		inputsChanged := false
		for i, in := range oldInputs {
			rootIn := c.findEqClassLocked(in)
			newInputs[i] = rootIn
			if rootIn != in {
				inputsChanged = true
			}
		}

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

func (c *cache) chooseTermFromSetLocked(set map[egraphTermID]struct{}, acceptResult func(*sharedResult) bool) *egraphTerm {
	var best *egraphTerm
	for termID := range set {
		term := c.egraphTerms[termID]
		if term == nil || term.result == nil {
			continue
		}
		if acceptResult != nil && !acceptResult(term.result) {
			continue
		}
		if best == nil || term.id < best.id {
			best = term
		}
	}
	return best
}

func (c *cache) lookupEquivalentResultLocked(proto callTermProto, acceptResult func(*sharedResult) bool) *sharedResult {
	if len(c.egraphTermsByDigest) == 0 {
		return nil
	}

	inputEqIDs := make([]eqClassID, len(proto.inputDigests))
	for i, inDig := range proto.inputDigests {
		classID, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			return nil
		}
		root := c.findEqClassLocked(classID)
		if root == 0 {
			return nil
		}
		inputEqIDs[i] = root
	}

	termDigest := calcEgraphTermDigest(proto.selfDigest, inputEqIDs)
	set := c.egraphTermsByDigest[termDigest]
	if len(set) == 0 {
		return nil
	}
	best := c.chooseTermFromSetLocked(set, acceptResult)
	if best == nil {
		return nil
	}
	return best.result
}

func (c *cache) resultHasTermDigestLocked(res *sharedResult, termDigest string) bool {
	termSet := c.egraphResultTerms[res]
	for termID := range termSet {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		if term.termDigest == termDigest {
			return true
		}
	}
	return false
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

func (c *cache) indexResultForIDLocked(id *call.ID, outputEqID eqClassID, res *sharedResult) {
	if id == nil || outputEqID == 0 || res == nil {
		return
	}

	proto, err := termProtoForID(id)
	if err != nil {
		slog.Warn("failed to derive e-graph term proto", "err", err)
		return
	}
	if len(proto.inputDigests) == 0 {
		return
	}

	inputEqIDs := make([]eqClassID, len(proto.inputDigests))
	for i, inDig := range proto.inputDigests {
		inputEqIDs[i] = c.findEqClassLocked(c.ensureEqClassForDigestLocked(inDig.String()))
	}
	termDigest := calcEgraphTermDigest(proto.selfDigest, inputEqIDs)

	if c.resultHasTermDigestLocked(res, termDigest) {
		c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)
		return
	}

	outputEqID = c.mergeOutputsForTermDigestLocked(termDigest, outputEqID)

	c.initEgraphLocked()
	termID := c.nextEgraphTermID
	c.nextEgraphTermID++

	term := &egraphTerm{
		id:         termID,
		selfDigest: proto.selfDigest,
		inputEqIDs: inputEqIDs,
		outputEqID: outputEqID,
		termDigest: termDigest,
		result:     res,
	}
	c.egraphTerms[termID] = term

	digestSet := c.egraphTermsByDigest[termDigest]
	if digestSet == nil {
		digestSet = make(map[egraphTermID]struct{})
		c.egraphTermsByDigest[termDigest] = digestSet
	}
	digestSet[termID] = struct{}{}

	for _, in := range inputEqIDs {
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

func (c *cache) outputEqClassForResultLocked(res *sharedResult) eqClassID {
	if res == nil {
		return 0
	}

	digestSet := make(map[string]struct{}, 6)
	add := func(dig string) {
		if dig == "" {
			return
		}
		digestSet[dig] = struct{}{}
	}

	if res.requestID != nil {
		add(res.requestID.Digest().String())
		for _, extra := range res.requestID.ExtraDigests() {
			if extra.Kind != call.ExtraDigestKindOutputEquivalence {
				continue
			}
			add(extra.Digest.String())
		}
	}
	if res.constructor != nil {
		add(res.constructor.Digest().String())
		for _, extra := range res.constructor.ExtraDigests() {
			if extra.Kind != call.ExtraDigestKindOutputEquivalence {
				continue
			}
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

func (c *cache) indexResultInEgraphLocked(res *sharedResult) {
	if res == nil {
		return
	}
	outputEqID := c.outputEqClassForResultLocked(res)
	if outputEqID == 0 {
		return
	}
	c.indexResultForIDLocked(res.requestID, outputEqID, res)
}

func (c *cache) aliasRequestIDToResultLocked(requestID *call.ID, res *sharedResult) {
	if requestID == nil || res == nil {
		return
	}
	outputEqID := c.outputEqClassForResultLocked(res)
	if outputEqID == 0 {
		return
	}
	if dig := requestID.Digest().String(); dig != "" {
		outputEqID = c.mergeEqClassesLocked(outputEqID, c.ensureEqClassForDigestLocked(dig))
	}
	for _, extra := range requestID.ExtraDigests() {
		if extra.Kind != call.ExtraDigestKindOutputEquivalence {
			continue
		}
		if d := extra.Digest.String(); d != "" {
			outputEqID = c.mergeEqClassesLocked(outputEqID, c.ensureEqClassForDigestLocked(d))
		}
	}
	c.indexResultForIDLocked(requestID, outputEqID, res)
}

func (c *cache) removeResultFromEgraphLocked(res *sharedResult) {
	if len(c.egraphTerms) == 0 {
		c.maybeResetEgraphLocked()
		return
	}
	if res == nil || len(c.egraphResultTerms) == 0 {
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
