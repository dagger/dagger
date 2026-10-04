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

// cloudCacheID is the identity an engine keys its one Cloud holding by. The
// Cloud's copy is not an engine cache's entry: an entry the Cloud holds still
// has a value of its own, or is a cached nil result.
const cloudCacheID CacheID = "cloud"

// CloudCacheID is the sending cache an engine's merges name: the Cloud.
const CloudCacheID = cloudCacheID

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
	// Replacements is the entry's replacement count in its cache, read with
	// the value state above; it orders that state (HeldValueState).
	Replacements uint64
}

// RemoteHoldingUpdate is what a later report adds to an existing holding: the
// parts an evaluation completed, the content digest it learned and the
// dependencies it added, at the entry's replacement count.
type RemoteHoldingUpdate struct {
	ContentDigest digest.Digest
	Deps          []uint64
	Parts         []PersistedPartAddress
	Replacements  uint64
}

// RemoteChange is what one operation on the Cloud cache's holdings changed,
// for the service to act on. Candidates are the holdings it created or
// released an owner of, for the next CollectRemoteHoldings: no operation
// collects by itself. Recipes are the recipe digests of the entries whose
// reconciliation it may change: an entry it created or gave a new holding, one
// whose holding's parts, count or expiry it changed, and every entry it
// joined to another class of entries, directly or by congruence
// (trackJoinsLocked). A recipe digest names the entry's class whenever
// EquivalentHolders reads it, whatever renumbering the classes had in between.
type RemoteChange struct {
	Candidates []HolderKey
	Recipes    []digest.Digest
	// recipes is the set of Recipes, which a merge's change can list by the
	// thousand.
	recipes map[digest.Digest]struct{}
}

// addRecipeOf adds entry's recipe digest to Recipes, once, in the order the
// entries were first added.
func (ch *RemoteChange) addRecipeOf(entry *sharedResult) {
	if len(entry.recipeKeys) == 0 {
		return
	}
	if ch.recipes == nil {
		ch.recipes = make(map[digest.Digest]struct{}, len(ch.Recipes)+1)
		for _, recipe := range ch.Recipes {
			ch.recipes[recipe] = struct{}{}
		}
	}
	recipe := entry.recipeKeys[0]
	if _, ok := ch.recipes[recipe]; !ok {
		ch.recipes[recipe] = struct{}{}
		ch.Recipes = append(ch.Recipes, recipe)
	}
}

// HeldValueState is one observation of the value state of a holding's copy:
// the counterpart entry's replacement count, read under the lock that read the
// rest, and that value's dependencies, complete and offered parts, own expiry
// (0: none) and content digest. A field the observation doesn't carry is
// empty.
//
// The count orders observations of value state, whatever order they arrive
// in (applyHeldValueStateLocked): an entry's parts go backward only when its
// value is replaced, and each replacement raises its count.
type HeldValueState struct {
	Replacements  uint64
	Deps          []uint64
	Parts         []PersistedPartAddress
	OfferedParts  []PersistedPartAddress
	ExpiresAtUnix int64
	ContentDigest digest.Digest
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

// heldPartState is how far a holding's cache has a part, in order of
// advance: a part with no state is pending.
type heldPartState uint8

const (
	// heldPartOffered: the holding's cache has an offer of the part, a
	// download description, and not the bytes yet.
	heldPartOffered heldPartState = iota + 1
	// heldPartComplete: the holding's cache has the part's bytes.
	heldPartComplete
)

type heldPart struct {
	state heldPartState
	// offer is set on an engine only, in its Cloud holding: the Cloud's copy
	// of the part, with its download addresses, renewal key and owner.
	offer *partOffer
}

// holding is what the cache knows about another cache's copy of an entry:
// on the Cloud, one engine cache's copy; on an engine, the Cloud's (see
// cloudHoldingLocked). Guarded by egraphMu.
type holding struct {
	// replacements is the counterpart entry's replacement count that the
	// value state describes: parts, expiresAtUnix, contentDigest and the
	// dependencies (applyHeldValueStateLocked). An engine's Cloud holding
	// doesn't use it.
	replacements uint64
	// expiresAtUnix is the copy's own expiry in its cache; the earlier
	// non-zero value wins, as the engine merges it.
	expiresAtUnix int64
	// unstored marks an engine's Cloud holding whose Cloud entry stores no
	// record: the Cloud offers parts it keeps for another entry of the
	// class. That copy has no value, so it keeps no terms (5.6). An engine
	// cache's copy always has a value.
	unstored bool
	// parts are the copy's complete and offered parts, by part address key.
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

// hasUnexpiredHoldingLocked reports whether another cache holds a copy of res
// that has a value and has not expired: a holding whose own expiry is unset
// or still ahead. Requires egraphMu.
func (res *sharedResult) hasUnexpiredHoldingLocked(nowUnix int64) bool {
	for _, h := range res.holders {
		if h.unstored {
			continue
		}
		if h.expiresAtUnix == 0 || nowUnix < h.expiresAtUnix {
			return true
		}
	}
	return false
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
// selected. A holding of an engine cache tells it from a cached nil result,
// which also has neither but is served, even when the Cloud holds it too.
// Requires egraphMu.
func (res *sharedResult) noValueLocked() bool {
	engineHolding := false
	for key := range res.holders {
		if key.Cache != cloudCacheID {
			engineHolding = true
			break
		}
	}
	if !engineHolding {
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
func (c *Cache) AttachRemoteHolding(ctx context.Context, key HolderKey, desc RemoteHolding) (RemoteChange, error) {
	var change RemoteChange
	if key.Cache == "" || key.Number == 0 || desc.Recipe == "" {
		return change, fmt.Errorf("attach remote holding: empty key or recipe")
	}
	if key.Cache == cloudCacheID {
		// An engine's Cloud holding owns nothing; merges and offers keep it.
		return change, fmt.Errorf("attach remote holding: %q is not an engine cache", key.Cache)
	}
	parts, err := heldPartKeys(desc.Parts)
	if err != nil {
		return change, fmt.Errorf("attach remote holding %s/%d: %w", key.Cache, key.Number, err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	state := c.remoteCacheLocked(key.Cache)
	joined := c.trackJoinsLocked()
	entry, h := c.holdingLocked(key)
	affected := false
	if h == nil {
		entry, h = c.newHoldingLocked(ctx, state, key, desc)
		change.Candidates = []HolderKey{key}
		affected = true
	}
	c.teachRemoteIdentityLocked(ctx, entry, desc.Field, desc.Recipe, desc.Request, desc.ContentDigest, desc.Term)
	if desc.Request != "" && desc.Request != desc.Recipe {
		if _, ok := c.entriesByRecipe[desc.Request]; !ok {
			c.indexRecipeLocked(desc.Request, entry)
		}
	}
	if desc.TypeName != "" {
		h.typeName = desc.TypeName
	}
	candidates, changed := c.applyHeldValueStateLocked(ctx, state, key, entry, h, HeldValueState{
		Replacements:  desc.Replacements,
		Deps:          desc.Deps,
		ExpiresAtUnix: desc.ExpiresAtUnix,
		ContentDigest: desc.ContentDigest,
	}, parts, nil)
	change.Candidates = append(change.Candidates, candidates...)
	if affected || changed {
		change.addRecipeOf(entry)
	}
	joined(&change)
	return change, nil
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
	return entry, c.addHoldingLocked(ctx, state, key, entry)
}

// addHoldingLocked attaches a new holding to entry and gives it its waiting
// dependents: holdings of its cache that named its number before it existed.
// Requires egraphMu for writing.
func (c *Cache) addHoldingLocked(ctx context.Context, state *remoteCacheState, key HolderKey, entry *sharedResult) *holding {
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
	return h
}

// ApplyMergedReply applies an engine cache's merged reply: for each record
// the Cloud sent, the entry it landed on in that cache holds the value of the
// record's Cloud entry, named by the record's SenderNumber. Merge on an engine
// runs in no session and no span names what it made, so the reply creates a
// holding that doesn't exist yet. Each target's value state is the reply's,
// at the target's replacement count: its actual dependencies, its complete
// and offered parts, and its own expiry. The roots' retention is one more
// observation, at the reply's generation and engine time. A record whose
// Cloud entry is gone is skipped.
//
// It collects nothing. It returns the holdings it created or released an
// owner of, for the next CollectRemoteHoldings, and the recipes it affected.
func (c *Cache) ApplyMergedReply(ctx context.Context, cache CacheID, sent ValueBundle, reply MergeReply) (RemoteChange, error) {
	var change RemoteChange
	if cache == "" || cache == cloudCacheID {
		return change, fmt.Errorf("apply merged reply: %q is not an engine cache", cache)
	}
	cloudEntries := make(map[TransferOrdinal]sharedResultID, len(sent.Values))
	for _, value := range sent.Values {
		cloudEntries[value.Ordinal] = sharedResultID(value.SenderNumber)
	}
	type target struct {
		value         MergedValue
		parts, offers []string
	}
	targets := make([]target, 0, len(reply.Values))
	for _, value := range reply.Values {
		if value.Number == 0 {
			return change, fmt.Errorf("apply merged reply: ordinal %d has no number", value.Ordinal)
		}
		if _, ok := cloudEntries[value.Ordinal]; !ok {
			return change, fmt.Errorf("apply merged reply: ordinal %d was not sent", value.Ordinal)
		}
		parts, err := heldPartKeys(value.Parts)
		if err != nil {
			return change, fmt.Errorf("apply merged reply: ordinal %d: %w", value.Ordinal, err)
		}
		offers, err := heldPartKeys(value.OfferedParts)
		if err != nil {
			return change, fmt.Errorf("apply merged reply: ordinal %d: %w", value.Ordinal, err)
		}
		targets = append(targets, target{value: value, parts: parts, offers: offers})
	}
	at := obsTime{generation: reply.Generation, engineTime: reply.EngineTimeUnixNano}

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	state := c.remoteCacheLocked(cache)
	for _, t := range targets {
		key := HolderKey{Cache: cache, Number: t.value.Number}
		entry, h := c.holdingLocked(key)
		created := false
		if h == nil {
			entry = c.resultsByID[cloudEntries[t.value.Ordinal]]
			if entry == nil {
				continue
			}
			h = c.addHoldingLocked(ctx, state, key, entry)
			change.Candidates = append(change.Candidates, key)
			created = true
		}
		candidates, changed := c.applyHeldValueStateLocked(ctx, state, key, entry, h, HeldValueState{
			Replacements:  t.value.Replacements,
			Deps:          t.value.Deps,
			ExpiresAtUnix: t.value.ExpiresAtUnix,
		}, t.parts, t.offers)
		change.Candidates = append(change.Candidates, candidates...)
		if created || changed {
			change.addRecipeOf(entry)
		}
	}
	for _, root := range reply.Roots {
		if root.Expired || root.Number == 0 {
			continue
		}
		key := HolderKey{Cache: cache, Number: root.Number}
		_, h := c.holdingLocked(key)
		if h == nil {
			continue
		}
		h.retention.observe(root.Retained, root.RetentionExpiresAtUnix, at)
		if !h.retention.retained {
			change.Candidates = append(change.Candidates, key)
		}
	}
	return change, nil
}

// UpdateRemoteHolding applies what a later report of an existing holding
// carries, completed parts, a learned content digest and dependencies, as one
// observation of its value state at the report's replacement count. It
// reports false, and changes nothing, when the cache has no such holding: a
// lazy span or share event never creates one. A higher count can drop
// dependencies, whose holdings it returns as candidates.
func (c *Cache) UpdateRemoteHolding(ctx context.Context, key HolderKey, update RemoteHoldingUpdate) (RemoteChange, bool, error) {
	var change RemoteChange
	parts, err := heldPartKeys(update.Parts)
	if err != nil {
		return change, false, fmt.Errorf("update remote holding %s/%d: %w", key.Cache, key.Number, err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		return change, false, nil
	}
	joined := c.trackJoinsLocked()
	candidates, changed := c.applyHeldValueStateLocked(ctx, c.remoteCacheLocked(key.Cache), key, entry, h, HeldValueState{
		Replacements:  update.Replacements,
		Deps:          update.Deps,
		ContentDigest: update.ContentDigest,
	}, parts, nil)
	change.Candidates = candidates
	if changed {
		change.addRecipeOf(entry)
	}
	joined(&change)
	return change, true, nil
}

// trackJoinsLocked starts collecting the entries that class unions join to
// another class of entries, the row's entry or any other, directly or by
// congruence. A union with a class no entry is in, such as a newly taught
// digest's, joins no entries: it changes no class's set of equivalent
// holdings. The returned function adds each joined entry's recipe digest to
// change and stops. Both run in one hold of egraphMu for writing.
func (c *Cache) trackJoinsLocked() func(change *RemoteChange) {
	c.joinedEntries = map[sharedResultID]struct{}{}
	return func(change *RemoteChange) {
		joined := make([]sharedResultID, 0, len(c.joinedEntries))
		for id := range c.joinedEntries {
			joined = append(joined, id)
		}
		c.joinedEntries = nil
		slices.Sort(joined)
		for _, id := range joined {
			if entry := c.resultsByID[id]; entry != nil {
				change.addRecipeOf(entry)
			}
		}
	}
}

// noteJoinedEntriesLocked records, while trackJoinsLocked collects, the
// entries of the class a union of two classes of entries has just formed:
// every one of them has joined another class. Requires egraphMu for writing.
func (c *Cache) noteJoinedEntriesLocked(root eqClassID) {
	if c.joinedEntries == nil {
		return
	}
	for id := range c.outputEqClassResults[root] {
		c.joinedEntries[id] = struct{}{}
	}
}

// applyHeldValueStateLocked applies one observation of h's value state by the
// replacement count it carries.
//   - A lower count describes a value the counterpart has replaced, and is
//     ignored, but for its content digest's class identity: the class learns
//     every observation's, whatever its count.
//   - A higher count replaces the value state: the parts, expiry and content
//     digest start from the observation's, and the dependencies become the
//     observation's. The holding releases its ownership of the dependencies
//     it drops, returned as candidates, and forgets its dropped unknown
//     numbers. Every observation that can raise the count carries the value's
//     dependencies, so they are never replaced with an empty set by mistake.
//   - An equal count merges: each part takes the more advanced state, the
//     earlier non-zero expiry wins, and the content digest and dependencies
//     are added. Within one value, an entry's dependencies only grow.
//
// parts and offered are the observation's complete and offered part address
// keys. It returns the dropped dependencies' holdings, and whether the
// holding's count, parts or expiry changed: the value state reconciliation
// reads. A content digest it teaches changes that only by joining classes,
// which trackJoinsLocked reports. Requires egraphMu for writing.
func (c *Cache) applyHeldValueStateLocked(ctx context.Context, state *remoteCacheState, key HolderKey, entry *sharedResult, h *holding, obs HeldValueState, parts, offered []string) ([]HolderKey, bool) {
	if obs.ContentDigest != "" && obs.ContentDigest != h.contentDigest {
		// The class learns a content digest from every observation, whatever
		// its count: a union is never undone, and gating it by count would
		// only make the class depend on arrival order. The holding's own
		// content digest follows the count below.
		c.teachRemoteIdentityLocked(ctx, entry, entry.description, "", "", obs.ContentDigest, nil)
	}
	if obs.Replacements < h.replacements {
		return nil, false
	}
	var candidates []HolderKey
	changed := false
	if obs.Replacements > h.replacements {
		changed = true
		h.replacements = obs.Replacements
		h.parts = nil
		h.expiresAtUnix = 0
		h.contentDigest = ""
		kept := make(map[uint64]struct{}, len(obs.Deps))
		for _, number := range obs.Deps {
			kept[number] = struct{}{}
		}
		for number := range h.deps {
			if _, ok := kept[number]; ok {
				continue
			}
			delete(h.deps, number)
			depKey := HolderKey{Cache: key.Cache, Number: number}
			if _, dep := c.holdingLocked(depKey); dep != nil {
				dep.dependents--
				candidates = append(candidates, depKey)
			}
		}
		for number := range h.unknownDeps {
			if _, ok := kept[number]; ok {
				continue
			}
			delete(h.unknownDeps, number)
			delete(state.unknownDependents[number], key.Number)
			if len(state.unknownDependents[number]) == 0 {
				delete(state.unknownDependents, number)
			}
		}
	}
	if expires := mergeSharedResultExpiryUnix(h.expiresAtUnix, obs.ExpiresAtUnix); expires != h.expiresAtUnix {
		h.expiresAtUnix = expires
		changed = true
	}
	if obs.ContentDigest != "" {
		h.contentDigest = obs.ContentDigest
	}
	if advanceHeldParts(h, offered, heldPartOffered) {
		changed = true
	}
	if advanceHeldParts(h, parts, heldPartComplete) {
		changed = true
	}
	c.addHoldingDepsLocked(state, key, h, obs.Deps)
	return candidates, changed
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
		deps, more, err := c.removeHoldingLocked(ctx, c.remoteCaches[key.Cache], key, entry, h, entries)
		queue = append(queue, deps...)
		entries = more
		collected = append(collected, key)
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

// removeHoldingLocked takes a holding off its entry: out of holderEntries and
// its cache's count, releasing its ownership of its holding dependencies,
// whose keys it returns, and forgetting its unknown numbers. It releases the
// holding's unit on its entry, appending to entries those left to collect.
// Requires egraphMu for writing.
func (c *Cache) removeHoldingLocked(ctx context.Context, state *remoteCacheState, key HolderKey, entry *sharedResult, h *holding, entries []*sharedResult) ([]HolderKey, []*sharedResult, error) {
	delete(entry.holders, key)
	delete(c.holderEntries, key)
	state.holdings--
	var deps []HolderKey
	for number := range h.deps {
		depKey := HolderKey{Cache: key.Cache, Number: number}
		if _, dep := c.holdingLocked(depKey); dep != nil {
			dep.dependents--
			deps = append(deps, depKey)
		}
	}
	for number := range h.unknownDeps {
		delete(state.unknownDependents[number], key.Number)
		if len(state.unknownDependents[number]) == 0 {
			delete(state.unknownDependents, number)
		}
	}
	entries, err := c.decrementIncomingOwnershipLocked(ctx, entry, entries)
	return deps, entries, err
}

// CollectRemoteHolding removes one holding outright, for a gone answer: its
// cache no longer has the entry, and a number names one entry for the life of
// its cache identity, so the answer is final. Its sessions' holds and its
// retention go with it, and its dependents' edges to it become unknown
// numbers, as for any dependency whose holding is gone. Its unit on its entry
// is released, and dagql's collection removes the entry if nothing else owns
// it. It returns as candidates the holdings it depended on, for the next
// CollectRemoteHoldings, which cascades; it reports false when the cache has
// no such holding. Entries it removes count toward the next check of the
// classes' compaction, which only CollectRemoteHoldings runs
// (compactEqClassesAfterCollectionLocked).
func (c *Cache) CollectRemoteHolding(ctx context.Context, key HolderKey) ([]HolderKey, bool, error) {
	c.egraphMu.Lock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		c.egraphMu.Unlock()
		return nil, false, nil
	}
	entriesBefore := len(c.resultsByID)
	state := c.remoteCaches[key.Cache]
	if h.dependents > 0 {
		for parentKey, id := range c.holderEntries {
			if parentKey.Cache != key.Cache {
				continue
			}
			parent := c.resultsByID[id].holders[parentKey]
			if _, ok := parent.deps[key.Number]; !ok {
				continue
			}
			delete(parent.deps, key.Number)
			if parent.unknownDeps == nil {
				parent.unknownDeps = make(map[uint64]struct{})
			}
			parent.unknownDeps[key.Number] = struct{}{}
			parents := state.unknownDependents[key.Number]
			if parents == nil {
				parents = make(map[uint64]struct{})
				state.unknownDependents[key.Number] = parents
			}
			parents[parentKey.Number] = struct{}{}
		}
	}
	for session := range h.sessions {
		delete(state.sessions[session], key.Number)
		if len(state.sessions[session]) == 0 {
			delete(state.sessions, session)
		}
	}
	candidates, entries, err := c.removeHoldingLocked(ctx, state, key, entry, h, nil)
	if state.holdings == 0 {
		delete(c.remoteCaches, key.Cache)
	}
	releases, collectErr := c.collectUnownedResultsLocked(ctx, entries)
	if len(c.resultsByID) < entriesBefore {
		c.eqClassRemoved = true
	}
	c.egraphMu.Unlock()
	slices.SortFunc(candidates, compareHolderKeys)
	return candidates, true, errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases))
}

// ApplyHeldValueState applies one observation of an existing holding's value
// state, such as an offered reply's, by its replacement count
// (applyHeldValueStateLocked). It never creates a holding: an observation
// about one that doesn't exist is dropped, and reports false.
func (c *Cache) ApplyHeldValueState(ctx context.Context, key HolderKey, obs HeldValueState) (RemoteChange, bool, error) {
	var change RemoteChange
	parts, err := heldPartKeys(obs.Parts)
	if err != nil {
		return change, false, fmt.Errorf("apply held value state %s/%d: %w", key.Cache, key.Number, err)
	}
	offered, err := heldPartKeys(obs.OfferedParts)
	if err != nil {
		return change, false, fmt.Errorf("apply held value state %s/%d: %w", key.Cache, key.Number, err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		return change, false, nil
	}
	joined := c.trackJoinsLocked()
	candidates, changed := c.applyHeldValueStateLocked(ctx, c.remoteCacheLocked(key.Cache), key, entry, h, obs, parts, offered)
	change.Candidates = candidates
	if changed {
		change.addRecipeOf(entry)
	}
	joined(&change)
	return change, true, nil
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

// advanceHeldParts raises each named part of h to at least state, complete
// over offered over pending, and reports whether any rose.
func advanceHeldParts(h *holding, keys []string, state heldPartState) bool {
	advanced := false
	for _, key := range keys {
		if h.parts == nil {
			h.parts = make(map[string]heldPart)
		}
		if part := h.parts[key]; part.state < state {
			part.state = state
			h.parts[key] = part
			advanced = true
		}
	}
	return advanced
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
	// Number is the entry's number in this cache.
	Number uint64
	// Stored reports that the entry stores a record, a value of its own, and
	// StoredExpiresAtUnix is that record's own expiry (0: none, or no
	// record).
	Stored              bool
	StoredExpiresAtUnix int64
	Field               string
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
	// Parts are the copy's complete parts, and OfferedParts the ones its
	// cache has an offer of and not the bytes.
	Parts        []PersistedPartAddress
	OfferedParts []PersistedPartAddress
	// Replacements is the counterpart's replacement count the value state
	// describes.
	Replacements uint64
}

// RemoteEntryInfo returns what the cache holds for the entry of one holding,
// read under one hold.
func (c *Cache) RemoteEntryInfo(key HolderKey) (RemoteEntryInfo, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		return RemoteEntryInfo{}, false
	}
	return c.remoteEntryInfoLocked(entry), true
}

// EntryInfo returns the same as RemoteEntryInfo for the entry numbered
// number, read under one hold. It reports false when the cache has no such
// entry.
func (c *Cache) EntryInfo(number uint64) (RemoteEntryInfo, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	entry := c.resultsByID[sharedResultID(number)]
	if entry == nil {
		return RemoteEntryInfo{}, false
	}
	return c.remoteEntryInfoLocked(entry), true
}

// remoteEntryInfoLocked describes entry: its number, whether it stores a
// record and that record's expiry, its identity and its holdings. Requires
// egraphMu.
func (c *Cache) remoteEntryInfoLocked(entry *sharedResult) RemoteEntryInfo {
	info := RemoteEntryInfo{Number: uint64(entry.id), Field: entry.description, Recipes: slices.Clone(entry.recipeKeys)}
	if !entry.noValueLocked() {
		info.Stored, info.StoredExpiresAtUnix = true, entry.expiresAtUnix
	}
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
	return info
}

func holdingInfo(key HolderKey, h *holding) RemoteHoldingInfo {
	info := RemoteHoldingInfo{
		Key:           key,
		TypeName:      h.typeName,
		ContentDigest: h.contentDigest,
		ExpiresAtUnix: h.expiresAtUnix,
		Retained:      h.retention.retained,
		Dependents:    h.dependents,
		Replacements:  h.replacements,
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
		if err := json.Unmarshal([]byte(partKey), &address); err != nil {
			continue
		}
		if h.parts[partKey].state == heldPartComplete {
			info.Parts = append(info.Parts, address)
		} else {
			info.OfferedParts = append(info.OfferedParts, address)
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
