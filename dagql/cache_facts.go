package dagql

import (
	"cmp"
	"slices"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// FactSink receives the cache's bookkeeping facts (see package cachefact).
//
// Emit is called with egraphMu held for writing, in fact-sequence order. It
// must not block and must not call back into the cache.
type FactSink interface {
	Emit(cachefact.Fact)
}

// CacheOption configures a Cache at construction.
type CacheOption func(*Cache)

// WithFactSink makes the cache emit its bookkeeping facts to sink. Without a
// sink the cache emits nothing.
func WithFactSink(sink FactSink) CacheOption {
	return func(c *Cache) {
		c.factSink = sink
	}
}

// WithEngineInstanceID names the engine instance that owns the cache. The
// debug snapshot reports it as engine_instance so its results can be joined
// with the facts the instance emitted.
func WithEngineInstanceID(id string) CacheOption {
	return func(c *Cache) {
		c.engineInstanceID = id
	}
}

// cacheFactState is the emitter's state on the cache. Every field is guarded
// by egraphMu.
type cacheFactState struct {
	factSink         FactSink
	engineInstanceID string
	// factSeq is the sequence number of the last emitted fact. It advances
	// under egraphMu at each emit site, so fact order is mutation order, and
	// the debug snapshot, written under the same lock, reports it as fact_seq.
	factSeq uint64
	// bootRestoredResults is the number of results the boot restore installed.
	bootRestoredResults int
}

func (c *Cache) factsEnabled() bool {
	return c != nil && c.factSink != nil
}

// emitFactLocked requires egraphMu for writing.
func (c *Cache) emitFactLocked(body cachefact.Body) {
	if !c.factsEnabled() {
		return
	}
	c.factSeq++
	c.factSink.Emit(cachefact.Fact{Seq: c.factSeq, Body: body})
}

// CacheResultNumber returns the engine-local result number of a cache-backed
// result: the number the cache's facts name it by.
func CacheResultNumber(res AnyResult) (uint64, bool) {
	if res == nil {
		return 0, false
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return 0, false
	}
	return uint64(shared.id), true
}

// BootRestoredResults returns the number of results the cache's boot restore
// installed.
func (c *Cache) BootRestoredResults() int {
	if c == nil {
		return 0
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.bootRestoredResults
}

// EmitFact emits one fact that describes no cache mutation, such as the
// engine's own start, liveness and stop, in sequence with the cache's facts.
func (c *Cache) EmitFact(body cachefact.Body) {
	if !c.factsEnabled() || body == nil {
		return
	}
	c.egraphMu.Lock()
	c.emitFactLocked(body)
	c.egraphMu.Unlock()
}

// postingRecorder collects the exact digest postings one mutation adds to a
// result, with their labels. A nil recorder records nothing.
type postingRecorder struct {
	digests []cachefact.Digest
}

func (c *Cache) newPostingRecorder() *postingRecorder {
	if !c.factsEnabled() {
		return nil
	}
	return &postingRecorder{}
}

// addLabeledResultDigestPostingLocked adds an exact posting and records it
// when it is new for the result. Requires egraphMu.
func (c *Cache) addLabeledResultDigestPostingLocked(resID sharedResultID, dig, label string, rec *postingRecorder) {
	before := len(c.resultIndexedDigests[resID])
	c.addResultDigestPostingLocked(resID, dig, resultDigestPostingExact)
	if rec != nil && len(c.resultIndexedDigests[resID]) > before {
		rec.digests = append(rec.digests, cachefact.Digest{Digest: dig, Label: label})
	}
}

// addFramePostingsLocked posts a frame's recipe digest and extra digests for
// the result. Requires egraphMu.
func (c *Cache) addFramePostingsLocked(resID sharedResultID, recipe digest.Digest, extras []call.ExtraDigest, rec *postingRecorder) {
	c.addLabeledResultDigestPostingLocked(resID, recipe.String(), cachefact.LabelRecipe, rec)
	for _, extra := range extras {
		c.addLabeledResultDigestPostingLocked(resID, extra.Digest.String(), extra.Label, rec)
	}
}

func factTerm(self digest.Digest, inputs []digest.Digest, provenance []egraphInputProvenanceKind) cachefact.Term {
	term := cachefact.Term{Self: self.String(), Inputs: make([]cachefact.TermInput, len(inputs))}
	for i, in := range inputs {
		term.Inputs[i].Digest = in.String()
		if i < len(provenance) {
			term.Inputs[i].Provenance = cachefact.Provenance(provenance[i])
		}
	}
	return term
}

func sortedResultIDs(ids map[sharedResultID]struct{}) []uint64 {
	out := make([]uint64, 0, len(ids))
	for id := range ids {
		out = append(out, uint64(id))
	}
	slices.Sort(out)
	return out
}

// factResultDescription fills the descriptive fields of a result fact.
func factResultDescription(res *sharedResult, fact *cachefact.Result) {
	fact.ID = uint64(res.id)
	fact.RecordType = res.recordType
	fact.CreatedAtUnixNano = res.createdAtUnixNano
	fact.ExpiresAtUnix = res.expiresAtUnix
	if frame := res.loadResultCall(); frame != nil {
		fact.Field = frame.Field
		if frame.Type != nil {
			fact.TypeName = frame.Type.NamedType
		}
	}
}

// factRetentionLocked fills the retention fields of a result fact from the
// result's installed edge. Requires egraphMu.
func (c *Cache) factRetentionLocked(resID sharedResultID, fact *cachefact.Result) {
	edge, ok := c.persistedEdgesByResult[resID]
	if !ok {
		return
	}
	fact.Retained = true
	fact.RetentionExpiresAtUnix = edge.expiresAtUnix
	fact.Unpruneable = edge.unpruneable
}

// announceResultLocked emits a result fact and marks the result announced, so
// its later deps, retention and removal facts are emitted. Requires egraphMu
// for writing.
func (c *Cache) announceResultLocked(res *sharedResult, fact cachefact.Result) {
	if !c.factsEnabled() || res == nil || res.id == 0 {
		return
	}
	if fact.Digests == nil {
		fact.Digests = []cachefact.Digest{}
	}
	if fact.Terms == nil {
		fact.Terms = []cachefact.Term{}
	}
	res.factAnnounced = true
	if fact.Origin != cachefact.OriginComputed {
		// Imported and restored results carry their dependencies here.
		res.factDepsAnnounced = true
	}
	c.emitFactLocked(fact)
}

// emitDepsLocked emits the result's complete dependency set and marks it
// announced; later explicit dependencies then emit the grown set. Requires
// egraphMu for writing.
func (c *Cache) emitDepsLocked(res *sharedResult) {
	if !c.factsEnabled() || res == nil || !res.factAnnounced {
		return
	}
	if cur := c.resultsByID[res.id]; cur != res {
		return
	}
	res.factDepsAnnounced = true
	c.emitFactLocked(cachefact.Deps{ID: uint64(res.id), Deps: sortedResultIDs(res.deps), Complete: true})
}

// emitRetentionLocked requires egraphMu for writing.
func (c *Cache) emitRetentionLocked(res *sharedResult, edge persistedEdge, retained bool) {
	if !c.factsEnabled() || res == nil || !res.factAnnounced {
		return
	}
	fact := cachefact.Retention{ID: uint64(res.id), Retained: retained}
	if retained {
		fact.ExpiresAtUnix = edge.expiresAtUnix
		fact.Unpruneable = edge.unpruneable
	}
	c.emitFactLocked(fact)
}

// emitRemovedLocked emits one removed fact for the announced results among
// removed. Requires egraphMu for writing.
func (c *Cache) emitRemovedLocked(removed []*sharedResult, reason cachefact.RemovedReason) {
	if !c.factsEnabled() || len(removed) == 0 {
		return
	}
	ids := make([]uint64, 0, len(removed))
	for _, res := range removed {
		if res == nil || !res.factAnnounced {
			continue
		}
		res.factAnnounced = false
		ids = append(ids, uint64(res.id))
	}
	if len(ids) == 0 {
		return
	}
	c.emitFactLocked(cachefact.Removed{IDs: ids, Reason: reason})
}

// classRepresentativeLocked returns the smallest digest of the class, the
// class's portable name in boot facts. Requires egraphMu.
func (c *Cache) classRepresentativeLocked(id eqClassID) string {
	root := c.findEqClassLocked(id)
	var rep string
	for dig := range c.eqClassToDigests[root] {
		if rep == "" || dig < rep {
			rep = dig
		}
	}
	return rep
}

// announceBootLocked describes the restored cache: one class fact per class,
// one term fact per term, then one result fact per result. Requires egraphMu
// for writing, after the restore fully succeeded.
func (c *Cache) announceBootLocked() {
	c.bootRestoredResults = len(c.resultsByID)
	if !c.factsEnabled() || len(c.resultsByID) == 0 {
		return
	}

	classIDs := make([]eqClassID, 0, len(c.eqClassToDigests))
	for id := range c.eqClassToDigests {
		if c.findEqClassLocked(id) == id {
			classIDs = append(classIDs, id)
		}
	}
	slices.SortFunc(classIDs, func(a, b eqClassID) int {
		return cmp.Compare(c.classRepresentativeLocked(a), c.classRepresentativeLocked(b))
	})
	for _, id := range classIDs {
		labels := make(map[string][]string)
		for extra := range c.eqClassExtraDigests[id] {
			labels[extra.Digest.String()] = append(labels[extra.Digest.String()], extra.Label)
		}
		var digests []cachefact.Digest
		for dig := range c.eqClassToDigests[id] {
			digLabels := labels[dig]
			if len(digLabels) == 0 {
				digLabels = []string{""}
			}
			for _, label := range digLabels {
				digests = append(digests, cachefact.Digest{Digest: dig, Label: label})
			}
		}
		if len(digests) == 0 {
			continue
		}
		slices.SortFunc(digests, func(a, b cachefact.Digest) int {
			return cmp.Or(cmp.Compare(a.Digest, b.Digest), cmp.Compare(a.Label, b.Label))
		})
		c.emitFactLocked(cachefact.Class{Digests: digests})
	}

	termIDs := make([]egraphTermID, 0, len(c.egraphTerms))
	for id := range c.egraphTerms {
		termIDs = append(termIDs, id)
	}
	slices.Sort(termIDs)
	for _, id := range termIDs {
		term := c.egraphTerms[id]
		if term == nil {
			continue
		}
		provenance := c.termInputProvenance[id]
		fact := cachefact.TermFact{
			Self:   term.selfDigest.String(),
			Inputs: make([]cachefact.TermInput, len(term.inputEqIDs)),
			Output: c.classRepresentativeLocked(term.outputEqID),
		}
		for i, in := range term.inputEqIDs {
			fact.Inputs[i].Digest = c.classRepresentativeLocked(in)
			if i < len(provenance) {
				fact.Inputs[i].Provenance = cachefact.Provenance(provenance[i])
			}
		}
		c.emitFactLocked(fact)
	}

	// Dependencies first, so every result fact names only results the stream
	// already announced.
	resultIDs := make([]sharedResultID, 0, len(c.resultsByID))
	for id := range c.resultsByID {
		resultIDs = append(resultIDs, id)
	}
	slices.Sort(resultIDs)
	visited := make(map[sharedResultID]bool, len(resultIDs))
	ordered := make([]*sharedResult, 0, len(resultIDs))
	var visit func(id sharedResultID)
	visit = func(id sharedResultID) {
		res := c.resultsByID[id]
		if res == nil || visited[id] {
			return
		}
		visited[id] = true
		for _, dep := range sortedResultIDs(res.deps) {
			visit(sharedResultID(dep))
		}
		ordered = append(ordered, res)
	}
	for _, id := range resultIDs {
		visit(id)
	}
	for _, res := range ordered {
		id := res.id
		fact := cachefact.Result{Origin: cachefact.OriginRestored}
		factResultDescription(res, &fact)
		reps := make([]string, 0, len(c.resultOutputEqClasses[id]))
		for classID := range c.outputEqClassesForResultLocked(id) {
			if rep := c.classRepresentativeLocked(classID); rep != "" {
				reps = append(reps, rep)
			}
		}
		slices.Sort(reps)
		for _, rep := range reps {
			fact.Digests = append(fact.Digests, cachefact.Digest{Digest: rep})
		}
		fact.Deps = sortedResultIDs(res.deps)
		c.factRetentionLocked(id, &fact)
		c.announceResultLocked(res, fact)
	}
}
