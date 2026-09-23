package dagql

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

// Remote entries let a cache built from other engines' cache facts hold those
// engines' results as entries that have identity, equivalence and dependencies
// but no value. They join classes through the ordinary e-graph code, exactly
// as the emitting engine's results did, and are never served as cache hits.

// RemoteEntryKey names one result of one engine: the engine instance and the
// engine-local result number its facts use.
type RemoteEntryKey struct {
	Engine string
	ID     uint64
}

func compareRemoteEntryKeys(a, b RemoteEntryKey) int {
	return cmp.Or(cmp.Compare(a.Engine, b.Engine), cmp.Compare(a.ID, b.ID))
}

// ErrUnknownRemoteEntry is returned for a fact about an entry the cache does
// not hold.
var ErrUnknownRemoteEntry = errors.New("unknown remote entry")

// errRemoteEntryHasNoValue refuses to load a remote entry by its result number.
var errRemoteEntryHasNoValue = errors.New("remote entry has no value")

// remoteEntryState marks a sharedResult as a remote entry. Guarded by egraphMu.
type remoteEntryState struct {
	key      RemoteEntryKey
	origin   cachefact.Origin
	typeName string
	// unknownDeps are dependencies a fact named that the cache does not hold.
	unknownDeps map[uint64]struct{}
	// removed records that the entry's own ownership unit was released: its
	// engine removed it. Dependents can still hold it.
	removed bool
}

// remoteEntriesLocked requires egraphMu.
func (c *Cache) remoteEntryLocked(key RemoteEntryKey) *sharedResult {
	id, ok := c.remoteEntries[key]
	if !ok {
		return nil
	}
	return c.resultsByID[id]
}

// UpsertRemoteEntry registers the entry a result fact of engine announces, and
// applies the fact's digests and terms through the engine's own identity
// step, once per term. A restored entry instead joins the classes its
// representative digests name, with broad postings, as a boot restore does.
// Dependencies the fact names (imported and restored entries) are attached; the
// numbers the cache does not hold are returned. Applying the same fact again
// changes nothing.
func (c *Cache) UpsertRemoteEntry(ctx context.Context, engine string, fact cachefact.Result) ([]uint64, error) {
	if engine == "" || fact.ID == 0 {
		return nil, fmt.Errorf("upsert remote entry: empty key")
	}
	key := RemoteEntryKey{Engine: engine, ID: fact.ID}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	if c.remoteEntries == nil {
		c.remoteEntries = make(map[RemoteEntryKey]sharedResultID)
	}
	res := c.remoteEntryLocked(key)
	if res == nil {
		res = &sharedResult{
			id:                c.nextSharedResultID,
			remote:            &remoteEntryState{key: key, origin: fact.Origin, typeName: fact.TypeName},
			expiresAtUnix:     fact.ExpiresAtUnix,
			createdAtUnixNano: fact.CreatedAtUnixNano,
			recordType:        fact.RecordType,
			description:       fact.Field,
		}
		c.nextSharedResultID++
		c.resultsByID[res.id] = res
		c.remoteEntries[key] = res.id
		// The entry's own ownership unit, released when its engine removes it.
		c.incrementIncomingOwnershipLocked(ctx, res)
	}
	if fact.Origin == cachefact.OriginRestored {
		c.joinRestoredClassesLocked(ctx, res, fact.Digests)
	} else {
		c.applyRemoteIdentityLocked(ctx, res, fact.Field, fact.Digests, fact.Terms)
	}
	return c.addRemoteDepsLocked(ctx, res, fact.Deps), nil
}

// TeachRemoteEntryIdentity applies an identity fact of engine to its entry:
// the digests it posted and the term it used, through the engine's identity
// step, which replays its reuse, association or creation of the term.
func (c *Cache) TeachRemoteEntryIdentity(ctx context.Context, engine string, fact cachefact.Identity) error {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res := c.remoteEntryLocked(RemoteEntryKey{Engine: engine, ID: fact.ID})
	if res == nil {
		return fmt.Errorf("%w: %s/%d", ErrUnknownRemoteEntry, engine, fact.ID)
	}
	c.applyRemoteIdentityLocked(ctx, res, res.description, fact.Digests, []cachefact.Term{fact.Term})
	res.expiresAtUnix = fact.ExpiresAtUnix
	return nil
}

// SetRemoteEntryDeps attaches the dependencies a deps fact of engine names,
// with dependency ownership, so the engine's collection rules hold. Sets only
// grow. The numbers the cache does not hold are returned.
func (c *Cache) SetRemoteEntryDeps(ctx context.Context, engine string, fact cachefact.Deps) ([]uint64, error) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res := c.remoteEntryLocked(RemoteEntryKey{Engine: engine, ID: fact.ID})
	if res == nil {
		return nil, fmt.Errorf("%w: %s/%d", ErrUnknownRemoteEntry, engine, fact.ID)
	}
	return c.addRemoteDepsLocked(ctx, res, fact.Deps), nil
}

// UpsertRemoteClass merges the digests of a restored class into one class,
// with their labels.
func (c *Cache) UpsertRemoteClass(ctx context.Context, fact cachefact.Class) error {
	if len(fact.Digests) == 0 {
		return nil
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	ids := make([]eqClassID, 0, len(fact.Digests))
	for _, d := range fact.Digests {
		if id := c.ensureEqClassForDigestLocked(ctx, d.Digest); id != 0 {
			ids = append(ids, id)
		}
	}
	root := c.mergeEqClassesLocked(ctx, ids...)
	if root == 0 {
		return nil
	}
	for _, d := range fact.Digests {
		if d.Label == "" || d.Digest == "" {
			continue
		}
		extras := c.eqClassExtraDigests[root]
		if extras == nil {
			extras = make(map[call.ExtraDigest]struct{})
			c.eqClassExtraDigests[root] = extras
		}
		extras[call.ExtraDigest{Digest: digest.Digest(d.Digest), Label: d.Label}] = struct{}{}
	}
	return nil
}

// UpsertRemoteTerm inserts a restored term over the classes of its input
// digests, producing the class of its output digest, with no entry attached.
// A congruent term that already exists has its output merged instead.
func (c *Cache) UpsertRemoteTerm(ctx context.Context, fact cachefact.TermFact) error {
	if fact.Self == "" || fact.Output == "" {
		return fmt.Errorf("upsert remote term: empty self or output")
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	inputs, provenance := factTermInputs(fact.Inputs)
	inputEqIDs := c.ensureTermInputEqIDsLocked(ctx, inputs)
	outputEqID := c.findEqClassLocked(c.ensureEqClassForDigestLocked(ctx, fact.Output))
	termDigest := calcEgraphTermDigest(digest.Digest(fact.Self), inputEqIDs)
	if existing := c.firstLiveTermInSetLocked(c.egraphTermsByTermDigest[termDigest]); existing != nil {
		c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID)
		return nil
	}
	term := c.insertTermLocked(ctx, digest.Digest(fact.Self), inputEqIDs, c.mergeOutputsForTermDigestLocked(ctx, termDigest, outputEqID))
	c.termInputProvenance[term.id] = provenance
	return nil
}

// RemoveRemoteEntry releases the entry's own ownership unit: its engine removed
// the result. When nothing else owns the entry it is collected, as the engine
// collects results, and its dependencies are released in turn.
func (c *Cache) RemoveRemoteEntry(ctx context.Context, key RemoteEntryKey) error {
	c.egraphMu.Lock()
	res := c.remoteEntryLocked(key)
	if res == nil || res.remote.removed {
		c.egraphMu.Unlock()
		return nil
	}
	res.remote.removed = true
	queue, err := c.decrementIncomingOwnershipLocked(ctx, res, nil)
	releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases))
}

// EquivalentRemoteEntries returns the remote entries in the class of digest.
func (c *Cache) EquivalentRemoteEntries(dig string) []RemoteEntryKey {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	classID, ok := c.egraphDigestToClass[dig]
	if !ok {
		return nil
	}
	var keys []RemoteEntryKey
	for id := range c.outputEqClassResults[c.eqClassRootLocked(classID)] {
		if res := c.resultsByID[id]; res != nil && res.remote != nil {
			keys = append(keys, res.remote.key)
		}
	}
	slices.SortFunc(keys, compareRemoteEntryKeys)
	return keys
}

// RemoteEntryClosure returns the entry and every entry it depends on,
// transitively.
func (c *Cache) RemoteEntryClosure(key RemoteEntryKey) []RemoteEntryKey {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	root := c.remoteEntryLocked(key)
	if root == nil {
		return nil
	}
	seen := map[sharedResultID]bool{root.id: true}
	queue := []*sharedResult{root}
	var keys []RemoteEntryKey
	for len(queue) > 0 {
		res := queue[0]
		queue = queue[1:]
		if res.remote != nil {
			keys = append(keys, res.remote.key)
		}
		for depID := range res.deps {
			if dep := c.resultsByID[depID]; dep != nil && !seen[depID] {
				seen[depID] = true
				queue = append(queue, dep)
			}
		}
	}
	slices.SortFunc(keys, compareRemoteEntryKeys)
	return keys
}

// RemoteEntryInfo describes one remote entry.
type RemoteEntryInfo struct {
	Key               RemoteEntryKey
	Origin            cachefact.Origin
	Field             string
	TypeName          string
	RecordType        string
	CreatedAtUnixNano int64
	ExpiresAtUnix     int64
	// Digests are the digests the entry is posted under.
	Digests []string
	// ClassDigests are every digest of the entry's equivalence classes.
	ClassDigests []string
	// Terms are the terms producing the entry's classes, each over its inputs'
	// class representatives (the smallest digest of each input class, as boot
	// facts name classes) with their provenance, sorted.
	Terms       []cachefact.Term
	Deps        []RemoteEntryKey
	UnknownDeps []uint64
	// Removed reports that the entry's engine removed it; entries that depend on
	// it keep it in the cache.
	Removed bool
}

// RemoteEntryInfo returns what the cache holds for one remote entry.
func (c *Cache) RemoteEntryInfo(key RemoteEntryKey) (RemoteEntryInfo, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.remoteEntryLocked(key)
	if res == nil {
		return RemoteEntryInfo{}, false
	}
	info := RemoteEntryInfo{
		Key:               key,
		Origin:            res.remote.origin,
		Field:             res.description,
		TypeName:          res.remote.typeName,
		RecordType:        res.recordType,
		CreatedAtUnixNano: res.createdAtUnixNano,
		ExpiresAtUnix:     res.expiresAtUnix,
		Removed:           res.remote.removed,
	}
	digests := map[string]struct{}{}
	for _, dig := range c.resultIndexedDigests[res.id] {
		digests[dig] = struct{}{}
	}
	_, broad := c.broadlyIndexedResults[res.id]
	classDigests := map[string]struct{}{}
	for classID := range c.outputEqClassRootsLocked(res.id) {
		for dig := range c.eqClassToDigests[classID] {
			classDigests[dig] = struct{}{}
			if broad {
				digests[dig] = struct{}{}
			}
		}
		for termID := range c.outputEqClassToTerms[classID] {
			if term := c.egraphTerms[termID]; term != nil {
				info.Terms = append(info.Terms, c.portableTermLocked(term))
			}
		}
	}
	info.Digests = sortedKeys(digests)
	info.ClassDigests = sortedKeys(classDigests)
	slices.SortFunc(info.Terms, compareTerms)
	for depID := range res.deps {
		if dep := c.resultsByID[depID]; dep != nil && dep.remote != nil {
			info.Deps = append(info.Deps, dep.remote.key)
		}
	}
	slices.SortFunc(info.Deps, compareRemoteEntryKeys)
	for id := range res.remote.unknownDeps {
		info.UnknownDeps = append(info.UnknownDeps, id)
	}
	slices.Sort(info.UnknownDeps)
	return info, true
}

// eqClassRootLocked returns the root of id's class, as findEqClassLocked
// does, without compressing the path, so a reader holding only
// egraphMu.RLock can use it.
func (c *Cache) eqClassRootLocked(id eqClassID) eqClassID {
	if id == 0 || int(id) >= len(c.egraphParents) {
		return 0
	}
	for c.egraphParents[id] != id {
		id = c.egraphParents[id]
	}
	return id
}

// outputEqClassRootsLocked is outputEqClassesForResultLocked for a reader
// holding only egraphMu.RLock.
func (c *Cache) outputEqClassRootsLocked(resID sharedResultID) map[eqClassID]struct{} {
	out := make(map[eqClassID]struct{}, len(c.resultOutputEqClasses[resID]))
	for eqID := range c.resultOutputEqClasses[resID] {
		if root := c.eqClassRootLocked(eqID); root != 0 {
			out[root] = struct{}{}
		}
	}
	return out
}

// portableTermLocked names a term's inputs by their class representatives.
// Requires egraphMu, for reading or writing.
func (c *Cache) portableTermLocked(term *egraphTerm) cachefact.Term {
	out := cachefact.Term{Self: term.selfDigest.String(), Inputs: make([]cachefact.TermInput, len(term.inputEqIDs))}
	provenance := c.termInputProvenance[term.id]
	for i, in := range term.inputEqIDs {
		out.Inputs[i].Digest = c.classRepresentativeLocked(in)
		if i < len(provenance) {
			out.Inputs[i].Provenance = cachefact.Provenance(provenance[i])
		}
	}
	return out
}

func compareTerms(a, b cachefact.Term) int {
	return cmp.Or(cmp.Compare(a.Self, b.Self), slices.CompareFunc(a.Inputs, b.Inputs, func(x, y cachefact.TermInput) int {
		return cmp.Or(cmp.Compare(x.Digest, y.Digest), cmp.Compare(x.Provenance, y.Provenance))
	}))
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func factTermInputs(inputs []cachefact.TermInput) ([]digest.Digest, []egraphInputProvenanceKind) {
	digests := make([]digest.Digest, len(inputs))
	provenance := make([]egraphInputProvenanceKind, len(inputs))
	for i, in := range inputs {
		digests[i] = digest.Digest(in.Digest)
		provenance[i] = egraphInputProvenanceKind(in.Provenance)
	}
	return digests, provenance
}

// applyRemoteIdentityLocked posts digests for the entry and applies each term
// through applyPreparedResultIdentityLocked. The frame it passes carries only
// the field name and the non-recipe digests, which that function reads as the
// request's extra digests; each recipe digest is applied as a request digest.
// Requires egraphMu for writing.
func (c *Cache) applyRemoteIdentityLocked(ctx context.Context, res *sharedResult, field string, digests []cachefact.Digest, terms []cachefact.Term) {
	var (
		recipes []digest.Digest
		extras  []call.ExtraDigest
	)
	for _, d := range digests {
		if d.Digest == "" {
			continue
		}
		if d.Label == cachefact.LabelRecipe {
			recipes = append(recipes, digest.Digest(d.Digest))
			continue
		}
		extras = append(extras, call.ExtraDigest{Digest: digest.Digest(d.Digest), Label: d.Label})
	}
	var primary digest.Digest
	if len(recipes) > 0 {
		primary = recipes[0]
	}
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, ExtraDigests: extras}
	bare := &ResultCall{Kind: ResultCallKindField, Field: field}
	for i, term := range terms {
		inputs, provenance := factTermInputs(term.Inputs)
		requestFrame := frame
		if i > 0 {
			requestFrame = bare
		}
		c.applyPreparedResultIdentityLocked(ctx, res, requestFrame, primary, digest.Digest(term.Self), inputs, provenance, primary)
	}
	if len(terms) == 0 {
		return
	}
	// Further recipe digests: the engine merged every digest of a result's
	// request and response frames into one class and posted each.
	inputs, provenance := factTermInputs(terms[0].Inputs)
	for _, recipe := range recipes[min(1, len(recipes)):] {
		c.applyPreparedResultIdentityLocked(ctx, res, bare, recipe, digest.Digest(terms[0].Self), inputs, provenance, recipe)
	}
}

// joinRestoredClassesLocked makes the entry an output of the classes its
// representative digests name, and posts every digest of those classes for
// it broadly, as a boot restore does. Requires egraphMu for writing.
func (c *Cache) joinRestoredClassesLocked(ctx context.Context, res *sharedResult, reps []cachefact.Digest) {
	for _, rep := range reps {
		if classID := c.ensureEqClassForDigestLocked(ctx, rep.Digest); classID != 0 {
			c.addResultOutputEqClassLocked(res.id, classID)
		}
	}
	outputs := c.outputEqClassesForResultLocked(res.id)
	if len(outputs) == 0 {
		return
	}
	c.markResultBroadlyIndexedLocked(res.id)
	for classID := range outputs {
		for dig := range c.eqClassToDigests[classID] {
			c.addResultDigestPostingLocked(res.id, dig, resultDigestPostingBroad)
		}
	}
}

// addRemoteDepsLocked requires egraphMu for writing.
func (c *Cache) addRemoteDepsLocked(ctx context.Context, res *sharedResult, deps []uint64) []uint64 {
	var unknown []uint64
	for _, depNum := range deps {
		dep := c.remoteEntryLocked(RemoteEntryKey{Engine: res.remote.key.Engine, ID: depNum})
		if dep == nil {
			if res.remote.unknownDeps == nil {
				res.remote.unknownDeps = make(map[uint64]struct{})
			}
			res.remote.unknownDeps[depNum] = struct{}{}
			unknown = append(unknown, depNum)
			continue
		}
		if dep.id == res.id {
			continue
		}
		if res.deps == nil {
			res.deps = make(map[sharedResultID]struct{})
		}
		if _, ok := res.deps[dep.id]; ok {
			continue
		}
		res.deps[dep.id] = struct{}{}
		res.dependencyOwnershipRevision++
		c.rememberDependencyEdgeLocked(res, dep)
		c.incrementIncomingOwnershipLocked(ctx, dep)
		delete(res.remote.unknownDeps, depNum)
	}
	return unknown
}

// insertTermLocked creates a term and indexes it by digest, input and output
// class, with no result attached. Requires egraphMu for writing.
func (c *Cache) insertTermLocked(ctx context.Context, self digest.Digest, inputEqIDs []eqClassID, outputEqID eqClassID) *egraphTerm {
	termID := c.nextEgraphTermID
	c.nextEgraphTermID++
	term := newEgraphTerm(termID, self, inputEqIDs, outputEqID)
	c.egraphTerms[termID] = term
	c.traceTermCreated(ctx, "remote", "", term)
	digestTerms := c.egraphTermsByTermDigest[term.termDigest]
	if digestTerms == nil {
		digestTerms = newEgraphTermIDSet()
		c.egraphTermsByTermDigest[term.termDigest] = digestTerms
	}
	digestTerms.Insert(termID)
	for _, inEqID := range term.inputEqIDs {
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
	outputTerms := c.outputEqClassToTerms[outputEqID]
	if outputTerms == nil {
		outputTerms = make(map[egraphTermID]struct{})
		c.outputEqClassToTerms[outputEqID] = outputTerms
	}
	outputTerms[termID] = struct{}{}
	return term
}
