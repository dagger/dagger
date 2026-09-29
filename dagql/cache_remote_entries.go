package dagql

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

// A cache built from other engine caches' telemetry, the Cloud cache, keeps
// what it knows of each engine cache's entries as holdings. One entry stands
// for one recipe, found by the recipe digest of the stored call frame; each
// holding on it is one engine cache's copy of that recipe. The entry carries
// identity in the e-graph, so lookups relate it to entries of other recipes
// through the ordinary class and term code. The holding carries that engine
// cache's own ownership of its copy: session holds, retention, and
// dependencies on the cache's other holdings. An entry known only through
// holdings has no value and is never served or loaded.
//
// The operations below only record what they change, and return as candidates
// the holdings they created or released an owner of. After applying one
// message, a batch of rows or a reply, the caller passes all its candidates to
// CollectRemoteHoldings, which collects those left with no owner.

// CacheID is an engine cache's identity: the random ID its persistence
// database keeps across the engine's restarts.
type CacheID string

// HolderKey names one entry of one engine cache: the cache's identity and the
// entry's result number there.
type HolderKey struct {
	Cache  CacheID
	Number uint64
}

func compareHolderKeys(a, b HolderKey) int {
	return cmp.Or(cmp.Compare(a.Cache, b.Cache), cmp.Compare(a.Number, b.Number))
}

// ErrUnknownHolding is returned for an operation on a holding the cache does
// not have.
var ErrUnknownHolding = errors.New("unknown holding")

// errEntryHasNoValue refuses to load an entry known only through holdings.
var errEntryHasNoValue = errors.New("entry has no value")

// RemoteTerm is an operation's self digest over its ordered structural inputs.
type RemoteTerm struct {
	Self   digest.Digest
	Inputs []digest.Digest
}

// RemoteHolding describes one engine cache's entry as one of its call spans
// reports it.
type RemoteHolding struct {
	// Recipe is the recipe digest of the entry's stored call frame
	// (dag.output); the holding sits on the entry of that recipe.
	Recipe digest.Digest
	// Request is the digest of the call that returned the entry (dag.digest),
	// taught onto the entry as an equivalence when it differs from Recipe.
	Request digest.Digest
	// Field is the name of the call's field, for description.
	Field string
	// Term is the call's structural term, when the call derived one.
	Term *RemoteTerm
	// ContentDigest is the entry's content digest, when known.
	ContentDigest digest.Digest
	// TypeName is the name of the entry's type.
	TypeName string
	// ExpiresAtUnix is the entry's own expiry in its cache (0: none).
	ExpiresAtUnix int64
	// Deps are the result numbers of the entry's dependencies in its cache.
	Deps []uint64
	// Parts are the entry's complete parts in its cache.
	Parts []PersistedPartAddress
}

// RemoteHoldingUpdate is what a later report adds to an existing holding: the
// parts an evaluation completed, the content digest it learned and the
// dependencies it added.
type RemoteHoldingUpdate struct {
	ContentDigest digest.Digest
	Deps          []uint64
	Parts         []PersistedPartAddress
}

// RetentionObservation is one observation of an entry's retention edge in its
// cache: set (Retained, with the edge's expiry) or dropped, at the time the
// engine observed it.
type RetentionObservation struct {
	Retained      bool
	ExpiresAtUnix int64
	// Generation is the number of the engine cache's start that observed it,
	// and EngineTimeUnixNano that engine's clock when it did.
	Generation         uint64
	EngineTimeUnixNano int64
}

// obsTime orders one engine cache's observations: every observation of a
// later generation is later than every observation of an earlier one,
// whatever the engines' clocks say.
type obsTime struct {
	generation uint64
	engineTime int64
}

func (t obsTime) compare(o obsTime) int {
	return cmp.Or(cmp.Compare(t.generation, o.generation), cmp.Compare(t.engineTime, o.engineTime))
}

// retentionRegister is a holding's retention as the latest observation left
// it.
type retentionRegister struct {
	observed      bool
	retained      bool
	expiresAtUnix int64
	at            obsTime
}

// observe applies one observation: the later one wins. On equal times a set
// beats a drop, and between two sets the earlier non-zero expiry wins, so the
// result does not depend on the order observations arrive in.
func (r *retentionRegister) observe(retained bool, expiresAtUnix int64, at obsTime) {
	if !retained {
		expiresAtUnix = 0
	}
	switch c := at.compare(r.at); {
	case !r.observed || c > 0:
	case c < 0:
		return
	case retained && !r.retained:
	case retained && r.retained:
		expiresAtUnix = mergeSharedResultExpiryUnix(r.expiresAtUnix, expiresAtUnix)
	default:
		return
	}
	*r = retentionRegister{observed: true, retained: retained, expiresAtUnix: expiresAtUnix, at: at}
}

type heldPartState uint8

const (
	// heldPartComplete: the holding's cache has the part's bytes.
	heldPartComplete heldPartState = iota + 1
)

type heldPart struct {
	state heldPartState
}

// holding is what the cache knows about one engine cache's copy of an entry.
// Guarded by egraphMu.
type holding struct {
	// expiresAtUnix is the copy's own expiry in its cache; the earlier
	// non-zero value wins, as the engine merges it.
	expiresAtUnix int64
	// parts are the copy's complete parts, by part address key.
	parts map[string]heldPart
	// unknownDeps are dependency numbers no holding of this cache has yet.
	unknownDeps map[uint64]struct{}
	retention   retentionRegister
	// sessions are the sessions of the holding's cache that hold the copy.
	sessions map[string]struct{}
	// deps are the dependency numbers that are holdings of this cache: each
	// owns its dependency's holding, counted in the dependency's dependents.
	deps       map[uint64]struct{}
	dependents int

	typeName      string
	contentDigest digest.Digest
}

func (h *holding) owned() bool {
	return len(h.sessions) > 0 || h.retention.retained || h.dependents > 0
}

// remoteCacheState is what the cache keeps for one held engine cache beside
// its holdings. Guarded by egraphMu.
type remoteCacheState struct {
	holdings int
	// unknownDependents maps each dependency number no holding of this cache
	// has yet to the numbers of the holdings that name it.
	unknownDependents map[uint64]map[uint64]struct{}
	// sessions maps each session to the numbers of the holdings it holds.
	sessions map[string]map[uint64]struct{}
}

// noValueLocked reports an entry known only through holdings: it has no value
// of its own, neither decoded nor stored, so it is never served, loaded or
// selected. The holdings tell it from an engine's cached nil result, which
// also has neither but is served. Requires egraphMu.
func (res *sharedResult) noValueLocked() bool {
	if len(res.holders) == 0 {
		return false
	}
	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	return !res.hasValue && res.persistedEnvelope == nil
}

// holdingLocked requires egraphMu.
func (c *Cache) holdingLocked(key HolderKey) (*sharedResult, *holding) {
	id, ok := c.holderEntries[key]
	if !ok {
		return nil, nil
	}
	entry := c.resultsByID[id]
	if entry == nil {
		return nil, nil
	}
	return entry, entry.holders[key]
}

// remoteCacheLocked returns the state of one held engine cache, creating it.
// Requires egraphMu for writing.
func (c *Cache) remoteCacheLocked(id CacheID) *remoteCacheState {
	if c.remoteCaches == nil {
		c.remoteCaches = make(map[CacheID]*remoteCacheState)
	}
	state := c.remoteCaches[id]
	if state == nil {
		state = &remoteCacheState{
			unknownDependents: make(map[uint64]map[uint64]struct{}),
			sessions:          make(map[string]map[uint64]struct{}),
		}
		c.remoteCaches[id] = state
	}
	return state
}

// indexRecipeLocked makes res the entry of recipe. Requires egraphMu for
// writing.
func (c *Cache) indexRecipeLocked(recipe digest.Digest, res *sharedResult) {
	if c.entriesByRecipe == nil {
		c.entriesByRecipe = make(map[digest.Digest]sharedResultID)
	}
	c.entriesByRecipe[recipe] = res.id
	res.recipeKeys = append(res.recipeKeys, recipe)
}

// unindexRecipesLocked removes the recipe keys that still name res. Requires
// egraphMu for writing.
func (c *Cache) unindexRecipesLocked(res *sharedResult) {
	for _, recipe := range res.recipeKeys {
		if c.entriesByRecipe[recipe] == res.id {
			delete(c.entriesByRecipe, recipe)
		}
	}
	res.recipeKeys = nil
}

// AttachRemoteHolding records an engine cache's entry as its call span reports
// it. The holding is found by key, or attached to the entry of its stored
// recipe, which is created when the cache has none; holdings that named the
// new holding's number as a dependency then own it. The entry learns the
// call's identity: the recipe, the request digest as an equivalence, the term
// and the content digest. The holding merges the expiry and adds the
// dependencies and complete parts. A holding it creates is a candidate for
// collection: it has no owner until a hold, retention or a dependent names it.
func (c *Cache) AttachRemoteHolding(ctx context.Context, key HolderKey, desc RemoteHolding) (candidates []HolderKey, _ error) {
	if key.Cache == "" || key.Number == 0 || desc.Recipe == "" {
		return nil, fmt.Errorf("attach remote holding: empty key or recipe")
	}
	parts, err := heldPartKeys(desc.Parts)
	if err != nil {
		return nil, fmt.Errorf("attach remote holding %s/%d: %w", key.Cache, key.Number, err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	state := c.remoteCacheLocked(key.Cache)
	entry, h := c.holdingLocked(key)
	if h == nil {
		entry, h = c.newHoldingLocked(ctx, state, key, desc)
		candidates = []HolderKey{key}
	}
	c.teachRemoteIdentityLocked(ctx, entry, desc.Field, desc.Recipe, desc.Request, desc.ContentDigest, desc.Term)
	if desc.Request != "" && desc.Request != desc.Recipe {
		if _, ok := c.entriesByRecipe[desc.Request]; !ok {
			c.indexRecipeLocked(desc.Request, entry)
		}
	}
	h.expiresAtUnix = mergeSharedResultExpiryUnix(h.expiresAtUnix, desc.ExpiresAtUnix)
	if desc.TypeName != "" {
		h.typeName = desc.TypeName
	}
	if desc.ContentDigest != "" {
		h.contentDigest = desc.ContentDigest
	}
	addHeldParts(h, parts)
	c.addHoldingDepsLocked(state, key, h, desc.Deps)
	return candidates, nil
}

// newHoldingLocked attaches a new holding to the entry of desc's recipe and
// gives it its waiting dependents. Requires egraphMu for writing.
func (c *Cache) newHoldingLocked(ctx context.Context, state *remoteCacheState, key HolderKey, desc RemoteHolding) (*sharedResult, *holding) {
	entry := c.resultsByID[c.entriesByRecipe[desc.Recipe]]
	if entry == nil {
		entry = &sharedResult{
			id:          c.nextSharedResultID,
			description: desc.Field,
		}
		c.nextSharedResultID++
		c.resultsByID[entry.id] = entry
		c.indexRecipeLocked(desc.Recipe, entry)
	}
	h := &holding{}
	if entry.holders == nil {
		entry.holders = make(map[HolderKey]*holding)
	}
	entry.holders[key] = h
	if c.holderEntries == nil {
		c.holderEntries = make(map[HolderKey]sharedResultID)
	}
	c.holderEntries[key] = entry.id
	// Each holding is one ownership unit of its entry.
	c.incrementIncomingOwnershipLocked(ctx, entry)
	state.holdings++
	for parent := range state.unknownDependents[key.Number] {
		if _, parentHolding := c.holdingLocked(HolderKey{Cache: key.Cache, Number: parent}); parentHolding != nil {
			delete(parentHolding.unknownDeps, key.Number)
			if parentHolding.deps == nil {
				parentHolding.deps = make(map[uint64]struct{})
			}
			parentHolding.deps[key.Number] = struct{}{}
			h.dependents++
		}
	}
	delete(state.unknownDependents, key.Number)
	return entry, h
}

// UpdateRemoteHolding adds what a later report of an existing holding carries:
// completed parts, a learned content digest and added dependencies. It reports
// false, and changes nothing, when the cache has no such holding.
func (c *Cache) UpdateRemoteHolding(ctx context.Context, key HolderKey, update RemoteHoldingUpdate) (bool, error) {
	parts, err := heldPartKeys(update.Parts)
	if err != nil {
		return false, fmt.Errorf("update remote holding %s/%d: %w", key.Cache, key.Number, err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		return false, nil
	}
	if update.ContentDigest != "" {
		c.teachRemoteIdentityLocked(ctx, entry, entry.description, "", "", update.ContentDigest, nil)
		h.contentDigest = update.ContentDigest
	}
	addHeldParts(h, parts)
	c.addHoldingDepsLocked(c.remoteCacheLocked(key.Cache), key, h, update.Deps)
	return true, nil
}

// AddRemoteHold records that session holds the holding.
func (c *Cache) AddRemoteHold(ctx context.Context, key HolderKey, session string) error {
	if session == "" {
		return fmt.Errorf("add remote hold: empty session")
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	_, h := c.holdingLocked(key)
	if h == nil {
		return fmt.Errorf("add remote hold: %w: %s/%d", ErrUnknownHolding, key.Cache, key.Number)
	}
	if h.sessions == nil {
		h.sessions = make(map[string]struct{})
	}
	h.sessions[session] = struct{}{}
	state := c.remoteCacheLocked(key.Cache)
	held := state.sessions[session]
	if held == nil {
		held = make(map[uint64]struct{})
		state.sessions[session] = held
	}
	held[key.Number] = struct{}{}
	return nil
}

// ReleaseRemoteSession releases every hold of one session of an engine cache.
// The holdings it held are candidates for collection.
func (c *Cache) ReleaseRemoteSession(ctx context.Context, cache CacheID, session string) []HolderKey {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.releaseRemoteSessionLocked(cache, session, nil)
}

// releaseRemoteSessionLocked appends the released holdings to candidates.
// Requires egraphMu for writing.
func (c *Cache) releaseRemoteSessionLocked(cache CacheID, session string, candidates []HolderKey) []HolderKey {
	state := c.remoteCaches[cache]
	if state == nil {
		return candidates
	}
	for number := range state.sessions[session] {
		key := HolderKey{Cache: cache, Number: number}
		if _, h := c.holdingLocked(key); h != nil {
			delete(h.sessions, session)
			candidates = append(candidates, key)
		}
	}
	delete(state.sessions, session)
	return candidates
}

// ObserveRemoteRetention applies one observation of the holding's retention
// edge: the latest observation, by generation and then engine time, sets it.
// A holding left without retention is a candidate for collection.
func (c *Cache) ObserveRemoteRetention(ctx context.Context, key HolderKey, obs RetentionObservation) ([]HolderKey, error) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	_, h := c.holdingLocked(key)
	if h == nil {
		return nil, fmt.Errorf("observe remote retention: %w: %s/%d", ErrUnknownHolding, key.Cache, key.Number)
	}
	h.retention.observe(obs.Retained, obs.ExpiresAtUnix, obsTime{generation: obs.Generation, engineTime: obs.EngineTimeUnixNano})
	if h.retention.retained {
		return nil, nil
	}
	return []HolderKey{key}, nil
}

// EndRemoteProcess ends the sessions of one process of an engine cache,
// releasing their holds. What its retention and dependencies own stays, under
// the same keys, for the cache's next process. The holdings the sessions held
// are candidates for collection.
func (c *Cache) EndRemoteProcess(ctx context.Context, cache CacheID, sessions []string) []HolderKey {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	var candidates []HolderKey
	for _, session := range sessions {
		candidates = c.releaseRemoteSessionLocked(cache, session, candidates)
	}
	return candidates
}

// ReleaseRemoteCache releases the holds and retention of every holding of an
// engine cache, and returns them all as candidates: collection removes them,
// through their dependencies on each other.
func (c *Cache) ReleaseRemoteCache(ctx context.Context, cache CacheID) []HolderKey {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	state := c.remoteCaches[cache]
	if state == nil {
		return nil
	}
	var candidates []HolderKey
	for key, id := range c.holderEntries {
		if key.Cache != cache {
			continue
		}
		h := c.resultsByID[id].holders[key]
		h.sessions = nil
		h.retention = retentionRegister{}
		candidates = append(candidates, key)
	}
	clear(state.sessions)
	slices.SortFunc(candidates, compareHolderKeys)
	return candidates
}

// CollectRemoteHoldings collects every candidate left with no session hold,
// no retention and no dependent, then the holdings only they owned, through
// holding dependencies. Each collected holding releases its entry's ownership
// unit, so entries left with no holding and no other owner are collected too.
// It returns the collected holdings' keys, sorted.
//
// It then compacts the e-graph's classes, at most once per
// eqClassCheckInterval and only when something changed since the last check
// (compactEqClassesAfterCollectionLocked), which renumbers them. Only the
// Cloud calls it, and never with snapshot sharing enabled, whose pending queue
// keeps class IDs. Engines compact through prune.
func (c *Cache) CollectRemoteHoldings(ctx context.Context, candidates []HolderKey) ([]HolderKey, error) {
	c.egraphMu.Lock()
	entriesBefore := len(c.resultsByID)
	var (
		queue     = slices.Clone(candidates)
		collected []HolderKey
		entries   []*sharedResult
		rerr      error
	)
	for len(queue) > 0 {
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		entry, h := c.holdingLocked(key)
		if h == nil || h.owned() {
			continue
		}
		state := c.remoteCaches[key.Cache]
		delete(entry.holders, key)
		delete(c.holderEntries, key)
		state.holdings--
		for number := range h.deps {
			depKey := HolderKey{Cache: key.Cache, Number: number}
			if _, dep := c.holdingLocked(depKey); dep != nil {
				dep.dependents--
				queue = append(queue, depKey)
			}
		}
		for number := range h.unknownDeps {
			delete(state.unknownDependents[number], key.Number)
			if len(state.unknownDependents[number]) == 0 {
				delete(state.unknownDependents, number)
			}
		}
		collected = append(collected, key)
		var err error
		entries, err = c.decrementIncomingOwnershipLocked(ctx, entry, entries)
		rerr = errors.Join(rerr, err)
	}
	for id, state := range c.remoteCaches {
		if state.holdings == 0 {
			delete(c.remoteCaches, id)
		}
	}
	releases, err := c.collectUnownedResultsLocked(ctx, entries)
	c.compactEqClassesAfterCollectionLocked(len(c.resultsByID) < entriesBefore)
	c.egraphMu.Unlock()
	slices.SortFunc(collected, compareHolderKeys)
	return collected, errors.Join(rerr, err, runOnReleaseFuncs(ctx, releases))
}

// eqClassSlotsLocked returns the class slots in use: class IDs start at 1.
// Requires egraphMu.
func (c *Cache) eqClassSlotsLocked() int {
	if len(c.egraphParents) == 0 {
		return 0
	}
	return len(c.egraphParents) - 1
}

// eqClassCheckInterval is the least time between two of the Cloud's
// compaction checks.
const eqClassCheckInterval = 10 * time.Second

// compactEqClassesAfterCollectionLocked runs the classes' non-forced
// compaction, which frees the classes no term or entry uses once they are at
// least half the slots. The check scans every term, entry and digest, so it
// runs at most once per eqClassCheckInterval, and only when something changed
// since the last check: the class slots differ, or a collection removed
// entries, removed telling whether this one did. A reset changes the slots
// too.
//
// The service collects at the end of every read, idle or not, so a pending
// check runs soon after its interval. A check costs one scan per interval,
// whatever the scan walks. At each check, the non-forced compaction frees the
// dead classes once they reach the live ones; between checks, the dead can
// exceed that by the classes allocated or made dead since the last check,
// until the first collection after the interval. Requires egraphMu for
// writing.
func (c *Cache) compactEqClassesAfterCollectionLocked(removed bool) {
	if removed {
		c.eqClassRemoved = true
	}
	if c.eqClassSlotsLocked() == c.eqClassCheckedSlots && !c.eqClassRemoved {
		return
	}
	clock := c.eqClassClock
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	if !c.eqClassCheckedAt.IsZero() && now.Sub(c.eqClassCheckedAt) < eqClassCheckInterval {
		return
	}
	start := time.Now()
	c.eqClassChecks++
	compacted, oldSlots, newSlots := c.compactEqClassesLocked(false)
	c.eqClassCheckedAt = now
	c.eqClassCheckedSlots = c.eqClassSlotsLocked()
	c.eqClassRemoved = false
	slog.Debug("dagql collection checked eq classes",
		"compacted", compacted,
		"oldSlots", oldSlots,
		"newSlots", newSlots,
		"duration", time.Since(start))
}

// addHoldingDepsLocked adds dependency numbers to a holding of the same cache:
// a number the cache holds is owned through that holding's dependents, and
// any other number waits in the reverse index until its holding is created.
// Requires egraphMu for writing.
func (c *Cache) addHoldingDepsLocked(state *remoteCacheState, key HolderKey, h *holding, deps []uint64) {
	for _, number := range deps {
		if number == key.Number {
			continue
		}
		if _, ok := h.deps[number]; ok {
			continue
		}
		if _, ok := h.unknownDeps[number]; ok {
			continue
		}
		if _, dep := c.holdingLocked(HolderKey{Cache: key.Cache, Number: number}); dep != nil {
			if h.deps == nil {
				h.deps = make(map[uint64]struct{})
			}
			h.deps[number] = struct{}{}
			dep.dependents++
			continue
		}
		if h.unknownDeps == nil {
			h.unknownDeps = make(map[uint64]struct{})
		}
		h.unknownDeps[number] = struct{}{}
		parents := state.unknownDependents[number]
		if parents == nil {
			parents = make(map[uint64]struct{})
			state.unknownDependents[number] = parents
		}
		parents[key.Number] = struct{}{}
	}
}

func heldPartKeys(addresses []PersistedPartAddress) ([]string, error) {
	keys := make([]string, 0, len(addresses))
	for _, address := range addresses {
		key, err := partAddressKey(address)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func addHeldParts(h *holding, keys []string) {
	for _, key := range keys {
		if h.parts == nil {
			h.parts = make(map[string]heldPart)
		}
		h.parts[key] = heldPart{state: heldPartComplete}
	}
}

// teachRemoteIdentityLocked makes the entry an output of the class of its
// recipe, the request digest and the content digest, and applies the term to
// that class, as a publication indexes a computed result. Requires egraphMu
// for writing.
func (c *Cache) teachRemoteIdentityLocked(ctx context.Context, entry *sharedResult, field string, recipe, request, content digest.Digest, term *RemoteTerm) {
	var extras []call.ExtraDigest
	if content != "" {
		extras = append(extras, call.ExtraDigest{Digest: content, Label: call.ExtraDigestLabelContent})
	}
	ids := make([]eqClassID, 0, 3)
	for classID := range c.outputEqClassesForResultLocked(entry.id) {
		ids = append(ids, classID)
	}
	for _, dig := range []digest.Digest{recipe, request, content} {
		if dig == "" {
			continue
		}
		ids = append(ids, c.ensureEqClassForDigestLocked(ctx, dig.String()))
		c.addResultDigestPostingLocked(entry.id, dig.String(), resultDigestPostingExact)
	}
	outputEqID := c.mergeEqClassesLocked(ctx, ids...)
	if outputEqID == 0 {
		return
	}
	c.addResultOutputEqClassLocked(entry.id, outputEqID)
	if len(extras) > 0 {
		outputEqID = c.findEqClassLocked(outputEqID)
		classExtras := c.eqClassExtraDigests[outputEqID]
		if classExtras == nil {
			classExtras = make(map[call.ExtraDigest]struct{})
			c.eqClassExtraDigests[outputEqID] = classExtras
		}
		for _, extra := range extras {
			classExtras[extra] = struct{}{}
		}
	}
	if term == nil || term.Self == "" {
		return
	}
	// The spans carry no input provenance; only persistence and debugging
	// read it.
	provenance := make([]egraphInputProvenanceKind, len(term.Inputs))
	for i := range provenance {
		provenance[i] = egraphInputProvenanceKindDigest
	}
	index := cmp.Or(request, recipe)
	c.applyPreparedResultIdentityLocked(ctx, entry, &ResultCall{Kind: ResultCallKindField, Field: field, ExtraDigests: extras}, index, term.Self, term.Inputs, provenance, index)
}

// EquivalentHolders returns the holdings on the entries of the class of
// digest, sorted.
func (c *Cache) EquivalentHolders(dig string) []HolderKey {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	classID, ok := c.egraphDigestToClass[dig]
	if !ok {
		return nil
	}
	var keys []HolderKey
	for id := range c.outputEqClassResults[c.eqClassRootLocked(classID)] {
		if res := c.resultsByID[id]; res != nil {
			for key := range res.holders {
				keys = append(keys, key)
			}
		}
	}
	slices.SortFunc(keys, compareHolderKeys)
	return keys
}

// HolderClosure returns the holding and every holding it depends on,
// transitively, through its cache's own dependencies: what that engine cache
// exports for it. It is empty when the cache has no such holding.
func (c *Cache) HolderClosure(key HolderKey) []HolderKey {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	if _, h := c.holdingLocked(key); h == nil {
		return nil
	}
	seen := map[uint64]bool{key.Number: true}
	queue := []uint64{key.Number}
	var keys []HolderKey
	for len(queue) > 0 {
		number := queue[0]
		queue = queue[1:]
		_, h := c.holdingLocked(HolderKey{Cache: key.Cache, Number: number})
		if h == nil {
			continue
		}
		keys = append(keys, HolderKey{Cache: key.Cache, Number: number})
		for dep := range h.deps {
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	slices.SortFunc(keys, compareHolderKeys)
	return keys
}

// RemoteEntryInfo describes an entry and every holding on it.
type RemoteEntryInfo struct {
	Field string
	// Recipes are the recipe digests that name the entry.
	Recipes []digest.Digest
	// Digests are the digests the entry is posted under.
	Digests []string
	// ClassDigests are every digest of the entry's equivalence classes.
	ClassDigests []string
	// Terms are the terms producing the entry's classes, each over its inputs'
	// class representatives (the smallest digest of each input class), sorted.
	Terms    []RemoteTerm
	Holdings []RemoteHoldingInfo
}

// RemoteHoldingInfo describes one holding.
type RemoteHoldingInfo struct {
	Key                    HolderKey
	TypeName               string
	ContentDigest          digest.Digest
	ExpiresAtUnix          int64
	Retained               bool
	RetentionExpiresAtUnix int64
	Sessions               []string
	Deps                   []uint64
	UnknownDeps            []uint64
	Dependents             int
	Parts                  []PersistedPartAddress
}

// RemoteEntryInfo returns what the cache holds for the entry of one holding.
func (c *Cache) RemoteEntryInfo(key HolderKey) (RemoteEntryInfo, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		return RemoteEntryInfo{}, false
	}
	info := RemoteEntryInfo{Field: entry.description, Recipes: slices.Clone(entry.recipeKeys)}
	slices.Sort(info.Recipes)
	digests := map[string]struct{}{}
	for _, dig := range c.resultIndexedDigests[entry.id] {
		digests[dig] = struct{}{}
	}
	classDigests := map[string]struct{}{}
	for classID := range c.outputEqClassRootsLocked(entry.id) {
		for dig := range c.eqClassToDigests[classID] {
			classDigests[dig] = struct{}{}
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
	for holderKey, held := range entry.holders {
		info.Holdings = append(info.Holdings, holdingInfo(holderKey, held))
	}
	slices.SortFunc(info.Holdings, func(a, b RemoteHoldingInfo) int { return compareHolderKeys(a.Key, b.Key) })
	return info, true
}

func holdingInfo(key HolderKey, h *holding) RemoteHoldingInfo {
	info := RemoteHoldingInfo{
		Key:           key,
		TypeName:      h.typeName,
		ContentDigest: h.contentDigest,
		ExpiresAtUnix: h.expiresAtUnix,
		Retained:      h.retention.retained,
		Dependents:    h.dependents,
	}
	if h.retention.retained {
		info.RetentionExpiresAtUnix = h.retention.expiresAtUnix
	}
	for session := range h.sessions {
		info.Sessions = append(info.Sessions, session)
	}
	slices.Sort(info.Sessions)
	for number := range h.deps {
		info.Deps = append(info.Deps, number)
	}
	slices.Sort(info.Deps)
	for number := range h.unknownDeps {
		info.UnknownDeps = append(info.UnknownDeps, number)
	}
	slices.Sort(info.UnknownDeps)
	partKeys := make([]string, 0, len(h.parts))
	for partKey := range h.parts {
		partKeys = append(partKeys, partKey)
	}
	slices.Sort(partKeys)
	for _, partKey := range partKeys {
		var address PersistedPartAddress
		if err := json.Unmarshal([]byte(partKey), &address); err == nil {
			info.Parts = append(info.Parts, address)
		}
	}
	return info
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

// classRepresentativeLocked returns the smallest digest of the class, its
// name in a portable term. Requires egraphMu, for reading or writing.
func (c *Cache) classRepresentativeLocked(id eqClassID) string {
	root := c.eqClassRootLocked(id)
	var rep string
	for dig := range c.eqClassToDigests[root] {
		if rep == "" || dig < rep {
			rep = dig
		}
	}
	return rep
}

// portableTermLocked names a term's inputs by their class representatives.
// Requires egraphMu, for reading or writing.
func (c *Cache) portableTermLocked(term *egraphTerm) RemoteTerm {
	out := RemoteTerm{Self: term.selfDigest, Inputs: make([]digest.Digest, len(term.inputEqIDs))}
	for i, in := range term.inputEqIDs {
		out.Inputs[i] = digest.Digest(c.classRepresentativeLocked(in))
	}
	return out
}

func compareTerms(a, b RemoteTerm) int {
	return cmp.Or(cmp.Compare(a.Self, b.Self), slices.Compare(a.Inputs, b.Inputs))
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
