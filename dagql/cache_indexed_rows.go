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

// Indexed rows let a cache built from other engines' cache facts hold those
// engines' results as rows that have identity, equivalence and dependencies
// but no value. They join classes through the ordinary e-graph code, exactly
// as the emitting engine's results did, and are never served as cache hits.

// RowKey names one result of one engine: the engine instance and the
// engine-local result number its facts use.
type RowKey struct {
	Engine string
	ID     uint64
}

func compareRowKeys(a, b RowKey) int {
	return cmp.Or(cmp.Compare(a.Engine, b.Engine), cmp.Compare(a.ID, b.ID))
}

// ErrUnknownIndexedRow is returned for a fact about a row the cache does not
// hold.
var ErrUnknownIndexedRow = errors.New("unknown indexed row")

// errIndexedRowHasNoValue refuses to load an indexed row by its result number.
var errIndexedRowHasNoValue = errors.New("indexed row has no value")

// indexedRowState marks a sharedResult as an indexed row. Guarded by egraphMu.
type indexedRowState struct {
	key      RowKey
	origin   cachefact.Origin
	typeName string
	// unknownDeps are dependencies a fact named that the cache does not hold.
	unknownDeps map[uint64]struct{}
	// removed records that the row's own ownership unit was released: its
	// engine removed it. Dependents can still hold it.
	removed bool
}

// indexedRowsLocked requires egraphMu.
func (c *Cache) indexedRowLocked(key RowKey) *sharedResult {
	id, ok := c.indexedRows[key]
	if !ok {
		return nil
	}
	return c.resultsByID[id]
}

// UpsertIndexedRow registers the row a result fact of engine announces, and
// applies the fact's digests and terms through the engine's own identity
// step, once per term. A restored row instead joins the classes its
// representative digests name, with broad postings, as a boot restore does.
// Dependencies the fact names (imported and restored rows) are attached; the
// numbers the cache does not hold are returned. Applying the same fact again
// changes nothing.
func (c *Cache) UpsertIndexedRow(ctx context.Context, engine string, fact cachefact.Result) ([]uint64, error) {
	if engine == "" || fact.ID == 0 {
		return nil, fmt.Errorf("upsert indexed row: empty key")
	}
	key := RowKey{Engine: engine, ID: fact.ID}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	if c.indexedRows == nil {
		c.indexedRows = make(map[RowKey]sharedResultID)
	}
	res := c.indexedRowLocked(key)
	if res == nil {
		res = &sharedResult{
			id:                c.nextSharedResultID,
			indexed:           &indexedRowState{key: key, origin: fact.Origin, typeName: fact.TypeName},
			expiresAtUnix:     fact.ExpiresAtUnix,
			createdAtUnixNano: fact.CreatedAtUnixNano,
			recordType:        fact.RecordType,
			description:       fact.Field,
		}
		c.nextSharedResultID++
		c.resultsByID[res.id] = res
		c.indexedRows[key] = res.id
		// The row's own ownership unit, released when its engine removes it.
		c.incrementIncomingOwnershipLocked(ctx, res)
	}
	if fact.Origin == cachefact.OriginRestored {
		c.joinRestoredClassesLocked(ctx, res, fact.Digests)
	} else {
		c.applyIndexedIdentityLocked(ctx, res, fact.Field, fact.Digests, fact.Terms)
	}
	return c.addIndexedDepsLocked(ctx, res, fact.Deps), nil
}

// TeachIndexedRowIdentity applies an identity fact of engine to its row:
// the digests it posted and the term it used, through the engine's identity
// step, which replays its reuse, association or creation of the term.
func (c *Cache) TeachIndexedRowIdentity(ctx context.Context, engine string, fact cachefact.Identity) error {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res := c.indexedRowLocked(RowKey{Engine: engine, ID: fact.ID})
	if res == nil {
		return fmt.Errorf("%w: %s/%d", ErrUnknownIndexedRow, engine, fact.ID)
	}
	c.applyIndexedIdentityLocked(ctx, res, res.description, fact.Digests, []cachefact.Term{fact.Term})
	res.expiresAtUnix = fact.ExpiresAtUnix
	return nil
}

// SetIndexedRowDeps attaches the dependencies a deps fact of engine names,
// with dependency ownership, so the engine's collection rules hold. Sets only
// grow. The numbers the cache does not hold are returned.
func (c *Cache) SetIndexedRowDeps(ctx context.Context, engine string, fact cachefact.Deps) ([]uint64, error) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res := c.indexedRowLocked(RowKey{Engine: engine, ID: fact.ID})
	if res == nil {
		return nil, fmt.Errorf("%w: %s/%d", ErrUnknownIndexedRow, engine, fact.ID)
	}
	return c.addIndexedDepsLocked(ctx, res, fact.Deps), nil
}

// UpsertIndexedClass merges the digests of a restored class into one class,
// with their labels.
func (c *Cache) UpsertIndexedClass(ctx context.Context, fact cachefact.Class) error {
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

// UpsertIndexedTerm inserts a restored term over the classes of its input
// digests, producing the class of its output digest, with no row attached.
// A congruent term that already exists has its output merged instead.
func (c *Cache) UpsertIndexedTerm(ctx context.Context, fact cachefact.TermFact) error {
	if fact.Self == "" || fact.Output == "" {
		return fmt.Errorf("upsert indexed term: empty self or output")
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

// RemoveIndexedRow releases the row's own ownership unit: its engine removed
// the result. When nothing else owns the row it is collected, as the engine
// collects results, and its dependencies are released in turn.
func (c *Cache) RemoveIndexedRow(ctx context.Context, key RowKey) error {
	c.egraphMu.Lock()
	res := c.indexedRowLocked(key)
	if res == nil || res.indexed.removed {
		c.egraphMu.Unlock()
		return nil
	}
	res.indexed.removed = true
	queue, err := c.decrementIncomingOwnershipLocked(ctx, res, nil)
	releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases))
}

// EquivalentRows returns the indexed rows in the class of digest.
func (c *Cache) EquivalentRows(dig string) []RowKey {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	classID, ok := c.egraphDigestToClass[dig]
	if !ok {
		return nil
	}
	var keys []RowKey
	for id := range c.outputEqClassResults[c.eqClassRootLocked(classID)] {
		if res := c.resultsByID[id]; res != nil && res.indexed != nil {
			keys = append(keys, res.indexed.key)
		}
	}
	slices.SortFunc(keys, compareRowKeys)
	return keys
}

// RowClosure returns the row and every row it depends on, transitively.
func (c *Cache) RowClosure(key RowKey) []RowKey {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	root := c.indexedRowLocked(key)
	if root == nil {
		return nil
	}
	seen := map[sharedResultID]bool{root.id: true}
	queue := []*sharedResult{root}
	var keys []RowKey
	for len(queue) > 0 {
		res := queue[0]
		queue = queue[1:]
		if res.indexed != nil {
			keys = append(keys, res.indexed.key)
		}
		for depID := range res.deps {
			if dep := c.resultsByID[depID]; dep != nil && !seen[depID] {
				seen[depID] = true
				queue = append(queue, dep)
			}
		}
	}
	slices.SortFunc(keys, compareRowKeys)
	return keys
}

// IndexedRowInfo describes one indexed row.
type IndexedRowInfo struct {
	Key               RowKey
	Origin            cachefact.Origin
	Field             string
	TypeName          string
	RecordType        string
	CreatedAtUnixNano int64
	ExpiresAtUnix     int64
	// Digests are the digests the row is posted under.
	Digests []string
	// ClassDigests are every digest of the row's equivalence classes.
	ClassDigests []string
	Deps         []RowKey
	UnknownDeps  []uint64
	// Removed reports that the row's engine removed it; rows that depend on
	// it keep it in the cache.
	Removed bool
}

// RowInfo returns what the cache holds for one indexed row.
func (c *Cache) RowInfo(key RowKey) (IndexedRowInfo, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.indexedRowLocked(key)
	if res == nil {
		return IndexedRowInfo{}, false
	}
	info := IndexedRowInfo{
		Key:               key,
		Origin:            res.indexed.origin,
		Field:             res.description,
		TypeName:          res.indexed.typeName,
		RecordType:        res.recordType,
		CreatedAtUnixNano: res.createdAtUnixNano,
		ExpiresAtUnix:     res.expiresAtUnix,
		Removed:           res.indexed.removed,
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
	}
	info.Digests = sortedKeys(digests)
	info.ClassDigests = sortedKeys(classDigests)
	for depID := range res.deps {
		if dep := c.resultsByID[depID]; dep != nil && dep.indexed != nil {
			info.Deps = append(info.Deps, dep.indexed.key)
		}
	}
	slices.SortFunc(info.Deps, compareRowKeys)
	for id := range res.indexed.unknownDeps {
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

// applyIndexedIdentityLocked posts digests for the row and applies each term
// through applyPreparedResultIdentityLocked. The frame it passes carries only
// the field name and the non-recipe digests, which that function reads as the
// request's extra digests; each recipe digest is applied as a request digest.
// Requires egraphMu for writing.
func (c *Cache) applyIndexedIdentityLocked(ctx context.Context, res *sharedResult, field string, digests []cachefact.Digest, terms []cachefact.Term) {
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

// joinRestoredClassesLocked makes the row an output of the classes its
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

// addIndexedDepsLocked requires egraphMu for writing.
func (c *Cache) addIndexedDepsLocked(ctx context.Context, res *sharedResult, deps []uint64) []uint64 {
	var unknown []uint64
	for _, depNum := range deps {
		dep := c.indexedRowLocked(RowKey{Engine: res.indexed.key.Engine, ID: depNum})
		if dep == nil {
			if res.indexed.unknownDeps == nil {
				res.indexed.unknownDeps = make(map[uint64]struct{})
			}
			res.indexed.unknownDeps[depNum] = struct{}{}
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
		delete(res.indexed.unknownDeps, depNum)
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
	c.traceTermCreated(ctx, "indexed", "", term)
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
