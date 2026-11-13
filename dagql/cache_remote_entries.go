package dagql

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unique"

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

// imageCacheID is the identity an engine keys its image holdings by: the
// entries the engine's image ships, which it merges at start. Like the Cloud
// holding, an image holding owns nothing, and it carries the image's offers.
// It is never reported to the Cloud nor renewed, and the engine never exports
// an entry it holds: every engine built from the image has it.
const imageCacheID CacheID = "image"

// ImageCacheID is the sending cache of the merge of the engine image's
// entries.
const ImageCacheID = imageCacheID

// senderCache reports whether cache names one of an engine's senders, the
// Cloud or its image, whose holdings own nothing, rather than an engine
// cache.
func senderCache(cache CacheID) bool {
	return cache == cloudCacheID || cache == imageCacheID
}

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
	// Executed reports that the call computed the entry, as the service
	// counts executed evidence: once the cache has dropped a value, only a
	// computation after the drop may bring it back (holding.executed).
	Executed bool
	// Image reports that the engine image ships the entry: every engine built
	// from the image has its parts, and none exports them. The holding takes
	// it from each call span, as an engine whose new image no longer ships
	// the entry reports (holding.image).
	Image bool
	// UsedAtUnixNano is when the span was recorded where the service read
	// it, a use of the entry (0: unknown). The entry's last use never moves
	// back.
	UsedAtUnixNano int64
	// Call is the call as its span encodes it (dagger.io/dag.call), when
	// the call returned the entry of another recipe (Request is not
	// Recipe). The entry keeps it, to send with the entry.
	Call string
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
// EquivalentHolders reads it, whatever joins and compactions the classes had
// in between.
//
// On a blob-backed cache, Holdings and Entries are what a reader that keeps
// its answers asks again (cache_change_marks.go), sorted, holdings by number
// and then cache:
//   - Holdings: every holding whose description (RemoteHoldingInfo's
//     Holding) may differ after the operation, those it created or removed
//     included;
//   - Entries: every entry of which any of these may differ: whether it
//     exists; its own facts, Stored, StoredExpiresAtUnix and Recipes; the
//     keys of its holdings; the entries of its classes (EntryClassEntries);
//     and any AvailablePart answer at one clock. A holding's description is
//     asked again when its entry is named too.
//
// Neither covers the digests, class digests, terms or field of an entry,
// which only EntryInfo returns.
type RemoteChange struct {
	Candidates []HolderKey
	Recipes    []digest.Digest
	Holdings   []HolderKey
	Entries    []uint64
	// Saved reports that the operation changed what a save of the cache
	// writes, beyond the holdings, which aren't saved: a call kept on a
	// stored entry, or the entry's expiry.
	Saved bool
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
	// key is the part's address key (partAddressKey).
	key unique.Handle[string]
	// offer is set on an engine only, in its Cloud holding: the Cloud's copy
	// of the part, with its download addresses, renewal key and owner.
	offer *partOffer
	state heldPartState
}

// holding is what the cache knows about another cache's copy of an entry:
// on the Cloud, one engine cache's copy; on an engine, the Cloud's (see
// cloudHoldingLocked). Its sets are sorted slices that are replaced, never
// changed in place (cache_held_sets.go). Guarded by egraphMu.
type holding struct {
	// replacements is the counterpart entry's replacement count that the
	// value state describes: parts, expiresAtUnix, contentDigest and the
	// dependencies (applyHeldValueStateLocked). An engine's Cloud holding
	// doesn't use it.
	replacements uint64
	// expiresAtUnix is the copy's own expiry in its cache; the earlier
	// non-zero value wins, as the engine merges it.
	expiresAtUnix int64
	retention     retentionRegister
	// parts are the copy's complete and offered parts, sorted by part
	// address key.
	parts []heldPart
	// deps are the dependency numbers that are holdings of this cache: each
	// owns its dependency's holding, counted in the dependency's dependents.
	deps []uint64
	// unknownDeps are dependency numbers no holding of this cache has yet.
	unknownDeps []uint64
	// sessions are the sessions of the holding's cache that hold the copy,
	// sorted.
	sessions []unique.Handle[string]

	typeName      unique.Handle[string]
	contentDigest unique.Handle[string]
	dependents    int32
	// unstored marks an engine's Cloud holding whose Cloud entry stores no
	// record: the Cloud offers parts it keeps for another entry of the
	// class. That copy has no value, so it keeps no terms (5.6). An engine
	// cache's copy always has a value.
	unstored bool
	// executed marks a holding whose cache computed the entry: a call span
	// with executed evidence named it. A prune that drops the entry's value
	// clears it on every holding of the entry, so the service takes a
	// dropped value back only from a cache that computed it after the drop;
	// copies from placement, and entries a cache only hit, are never
	// flagged.
	executed bool
	// image marks a holding whose cache has the entry from its engine image,
	// as its latest call span reported (RemoteHolding.Image). The engine
	// never exports such an entry or its parts.
	image bool
}

// hasUnexpiredHoldingLocked reports whether another cache holds a copy of res
// that has a value and has not expired: a holding whose own expiry is unset
// or still ahead. Requires egraphMu.
func (res *sharedResult) hasUnexpiredHoldingLocked(nowUnix int64) bool {
	for _, ref := range res.holderRefsLocked() {
		h := ref.h
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
	// graphVersion counts the changes to the cache's holdings and their
	// dependencies: a holding added or removed, a dependency added or
	// dropped. HolderClosure reads a closure in pieces, and starts again
	// when it changed between two of them.
	graphVersion uint64
	// entries maps each holding's number to the entry it sits on.
	entries map[uint64]sharedResultID
	// unknownDependents maps each dependency number no holding of this cache
	// has yet to the numbers of the holdings that name it, sorted.
	unknownDependents map[uint64][]uint64
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
	for _, ref := range res.holderRefsLocked() {
		if !senderCache(ref.key.Cache) {
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
	state := c.remoteCaches[key.Cache]
	if state == nil {
		return nil, nil
	}
	id, ok := state.entries[key.Number]
	if !ok {
		return nil, nil
	}
	entry := c.resultsByID[id]
	if entry == nil {
		return nil, nil
	}
	return entry, entry.holderLocked(key)
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
			entries:           make(map[uint64]sharedResultID),
			unknownDependents: make(map[uint64][]uint64),
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
	c.markEntryLocked(res.id)
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
	parts, kept, err := prepareRemoteHolding(key, desc)
	if err != nil {
		return RemoteChange{}, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.attachRemoteHoldingLocked(ctx, key, desc, parts, kept), nil
}

// prepareRemoteHolding checks a holding's key and recipe, and derives what
// attaching it reads of its description without the cache: its parts' keys
// and the call it keeps.
func prepareRemoteHolding(key HolderKey, desc RemoteHolding) ([]string, *keptCall, error) {
	if key.Cache == "" || key.Number == 0 || desc.Recipe == "" {
		return nil, nil, fmt.Errorf("attach remote holding: empty key or recipe")
	}
	if senderCache(key.Cache) {
		// An engine's Cloud and image holdings own nothing; merges and offers
		// keep them.
		return nil, nil, fmt.Errorf("attach remote holding: %q is not an engine cache", key.Cache)
	}
	parts, err := heldPartKeys(desc.Parts)
	if err != nil {
		return nil, nil, fmt.Errorf("attach remote holding %s/%d: %w", key.Cache, key.Number, err)
	}
	kept, err := newKeptCall(desc)
	if err != nil {
		// The holding is what the span reports; a call this cache can't
		// derive is left out, and sent nowhere.
		slog.Warn("remote holding call left out", "cache", key.Cache, "number", key.Number, "error", err)
		kept = nil
	}
	return parts, kept, nil
}

// attachRemoteHoldingLocked is AttachRemoteHolding's change, with desc's
// parts' keys and kept call (prepareRemoteHolding). Requires egraphMu for
// writing.
func (c *Cache) attachRemoteHoldingLocked(ctx context.Context, key HolderKey, desc RemoteHolding, parts []string, kept *keptCall) (change RemoteChange) {
	defer c.takeChangesLocked(&change)
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
	change.Saved = c.keepCallLocked(entry, desc, kept)
	if desc.TypeName != "" && desc.TypeName != handleString(h.typeName) {
		h.typeName = internString(desc.TypeName)
		c.markHoldingLocked(key)
	}
	if desc.Executed && !h.executed {
		h.executed = true
		c.markHoldingLocked(key)
	}
	if h.image != desc.Image {
		h.image = desc.Image
		c.markHoldingLocked(key)
	}
	touchSharedResultLastUsed(entry, desc.UsedAtUnixNano)
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
	return change
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
		c.putResultLocked(entry)
		c.indexRecipeLocked(desc.Recipe, entry)
	}
	return entry, c.addHoldingLocked(ctx, state, key, entry)
}

// addHoldingLocked attaches a new holding to entry and gives it its waiting
// dependents: holdings of its cache that named its number before it existed.
// Requires egraphMu for writing.
func (c *Cache) addHoldingLocked(ctx context.Context, state *remoteCacheState, key HolderKey, entry *sharedResult) *holding {
	h := &holding{}
	state.graphVersion++
	entry.putHolderLocked(key, h)
	c.markHoldingLocked(key)
	c.markEntryLocked(entry.id)
	state.entries[key.Number] = entry.id
	// Each holding is one ownership unit of its entry.
	c.incrementIncomingOwnershipLocked(ctx, entry)
	state.holdings++
	for _, parent := range state.unknownDependents[key.Number] {
		if _, parentHolding := c.holdingLocked(HolderKey{Cache: key.Cache, Number: parent}); parentHolding != nil {
			parentHolding.unknownDeps, _ = withoutNumber(parentHolding.unknownDeps, key.Number)
			parentHolding.deps = withNumber(parentHolding.deps, key.Number)
			c.markHoldingLocked(HolderKey{Cache: key.Cache, Number: parent})
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
	cloudEntries, targets, err := prepareMergedReply(cache, sent, reply)
	if err != nil {
		return RemoteChange{}, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.applyMergedReplyLocked(ctx, cache, cloudEntries, targets, reply), nil
}

// mergedTarget is a record of a merged reply, with its complete and offered
// part address keys.
type mergedTarget struct {
	value         MergedValue
	parts, offers []string
}

// prepareMergedReply checks a merged reply against the bundle sent, and
// returns the Cloud entry of each of the bundle's ordinals and the reply's
// records, ready to apply. It reads nothing of the cache.
func prepareMergedReply(cache CacheID, sent ValueBundle, reply MergeReply) (map[TransferOrdinal]sharedResultID, []mergedTarget, error) {
	if cache == "" || senderCache(cache) {
		return nil, nil, fmt.Errorf("apply merged reply: %q is not an engine cache", cache)
	}
	cloudEntries := make(map[TransferOrdinal]sharedResultID, len(sent.Values))
	for _, value := range sent.Values {
		cloudEntries[value.Ordinal] = sharedResultID(value.SenderNumber)
	}
	targets := make([]mergedTarget, 0, len(reply.Values))
	for _, value := range reply.Values {
		if value.Number == 0 {
			return nil, nil, fmt.Errorf("apply merged reply: ordinal %d has no number", value.Ordinal)
		}
		if _, ok := cloudEntries[value.Ordinal]; !ok {
			return nil, nil, fmt.Errorf("apply merged reply: ordinal %d was not sent", value.Ordinal)
		}
		parts, err := heldPartKeys(value.Parts)
		if err != nil {
			return nil, nil, fmt.Errorf("apply merged reply: ordinal %d: %w", value.Ordinal, err)
		}
		offers, err := heldPartKeys(value.OfferedParts)
		if err != nil {
			return nil, nil, fmt.Errorf("apply merged reply: ordinal %d: %w", value.Ordinal, err)
		}
		targets = append(targets, mergedTarget{value: value, parts: parts, offers: offers})
	}
	return cloudEntries, targets, nil
}

// applyMergedReplyLocked applies a merged reply prepared by
// prepareMergedReply, as ApplyMergedReply describes. Requires egraphMu for
// writing.
func (c *Cache) applyMergedReplyLocked(ctx context.Context, cache CacheID, cloudEntries map[TransferOrdinal]sharedResultID, targets []mergedTarget, reply MergeReply) (change RemoteChange) {
	defer c.takeChangesLocked(&change)
	c.applyMergedTargetsLocked(ctx, cache, cloudEntries, targets, &change)
	c.applyMergedRootsLocked(cache, reply, &change)
	return change
}

// applyMergedTargetsLocked lands a merged reply's records, targets, on the
// engine cache's holdings, as ApplyMergedReply describes, and adds what it
// changed to change. Requires egraphMu for writing.
func (c *Cache) applyMergedTargetsLocked(ctx context.Context, cache CacheID, cloudEntries map[TransferOrdinal]sharedResultID, targets []mergedTarget, change *RemoteChange) {
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
}

// applyMergedRootsLocked observes a merged reply's roots' retention, at the
// reply's generation and engine time, on the holdings its records landed
// on, and adds what it changed to change. Requires egraphMu for writing.
func (c *Cache) applyMergedRootsLocked(cache CacheID, reply MergeReply, change *RemoteChange) {
	at := obsTime{generation: reply.Generation, engineTime: reply.EngineTimeUnixNano}
	for _, root := range reply.Roots {
		if root.Expired || root.Number == 0 {
			continue
		}
		key := HolderKey{Cache: cache, Number: root.Number}
		_, h := c.holdingLocked(key)
		if h == nil {
			continue
		}
		before := h.retention
		h.retention.observe(root.Retained, root.RetentionExpiresAtUnix, at)
		if h.retention.retained != before.retained || h.retention.expiresAtUnix != before.expiresAtUnix {
			c.markHoldingLocked(key)
		}
		if !h.retention.retained {
			change.Candidates = append(change.Candidates, key)
		}
	}
}

// MergedReplyApply is an engine cache's merged reply prepared to apply in
// pieces (RemoteWrites.ApplyMergedReplyPiece), each in a hold of the
// cache's lock of its own, so that the cache's readers get in between the
// pieces of a large reply. A reader between two pieces sees the holdings of
// the records applied so far, each in its final value state but for its
// dependencies on records still to come, which it names as unknown until
// they land, its dependents, and its retention, which the last piece
// observes: less than the engine holds, never other. Applied whole, piece
// by piece, it changes the cache as ApplyMergedReply does.
type MergedReplyApply struct {
	cache        CacheID
	cloudEntries map[TransferOrdinal]sharedResultID
	targets      []mergedTarget
	reply        MergeReply
	next         int
}

// PrepareMergedReply checks and prepares an engine cache's merged reply,
// as ApplyMergedReply does before its lock, to apply in pieces. It reads
// nothing of the cache.
func PrepareMergedReply(cache CacheID, sent ValueBundle, reply MergeReply) (*MergedReplyApply, error) {
	cloudEntries, targets, err := prepareMergedReply(cache, sent, reply)
	if err != nil {
		return nil, err
	}
	return &MergedReplyApply{cache: cache, cloudEntries: cloudEntries, targets: targets, reply: reply}, nil
}

// Records returns how many records the reply lands.
func (a *MergedReplyApply) Records() int {
	return len(a.targets)
}

// UpdateRemoteHolding applies what a later report of an existing holding
// carries, completed parts, a learned content digest and dependencies, as one
// observation of its value state at the report's replacement count. It
// reports false, and changes nothing, when the cache has no such holding: a
// lazy span or share event never creates one. A higher count can drop
// dependencies, whose holdings it returns as candidates.
func (c *Cache) UpdateRemoteHolding(ctx context.Context, key HolderKey, update RemoteHoldingUpdate) (RemoteChange, bool, error) {
	parts, err := prepareRemoteHoldingUpdate(key, update)
	if err != nil {
		return RemoteChange{}, false, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	change, found := c.updateRemoteHoldingLocked(ctx, key, update, parts)
	return change, found, nil
}

// prepareRemoteHoldingUpdate derives update's parts' keys.
func prepareRemoteHoldingUpdate(key HolderKey, update RemoteHoldingUpdate) ([]string, error) {
	parts, err := heldPartKeys(update.Parts)
	if err != nil {
		return nil, fmt.Errorf("update remote holding %s/%d: %w", key.Cache, key.Number, err)
	}
	return parts, nil
}

// updateRemoteHoldingLocked is UpdateRemoteHolding's change, with update's
// parts' keys. Requires egraphMu for writing.
func (c *Cache) updateRemoteHoldingLocked(ctx context.Context, key HolderKey, update RemoteHoldingUpdate, parts []string) (change RemoteChange, _ bool) {
	defer c.takeChangesLocked(&change)
	entry, h := c.holdingLocked(key)
	if h == nil {
		return change, false
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
	return change, true
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
	if obs.ContentDigest != "" && string(obs.ContentDigest) != handleString(h.contentDigest) {
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
		h.contentDigest = unique.Handle[string]{}
		kept := make(map[uint64]struct{}, len(obs.Deps))
		for _, number := range obs.Deps {
			kept[number] = struct{}{}
		}
		h.deps = keepNumbers(h.deps, func(number uint64) bool {
			if _, ok := kept[number]; ok {
				return true
			}
			state.graphVersion++
			depKey := HolderKey{Cache: key.Cache, Number: number}
			if _, dep := c.holdingLocked(depKey); dep != nil {
				dep.dependents--
				c.markHoldingLocked(depKey)
				candidates = append(candidates, depKey)
			}
			return false
		})
		h.unknownDeps = keepNumbers(h.unknownDeps, func(number uint64) bool {
			if _, ok := kept[number]; ok {
				return true
			}
			state.forgetUnknownDependent(number, key.Number)
			return false
		})
	}
	if expires := mergeSharedResultExpiryUnix(h.expiresAtUnix, obs.ExpiresAtUnix); expires != h.expiresAtUnix {
		h.expiresAtUnix = expires
		changed = true
	}
	described := changed
	if obs.ContentDigest != "" && string(obs.ContentDigest) != handleString(h.contentDigest) {
		h.contentDigest = internString(string(obs.ContentDigest))
		described = true
	}
	if advanceHeldParts(h, offered, heldPartOffered) {
		changed = true
	}
	if advanceHeldParts(h, parts, heldPartComplete) {
		changed = true
	}
	if described || changed {
		c.markHoldingLocked(key)
	}
	c.addHoldingDepsLocked(state, key, h, obs.Deps)
	return candidates, changed
}

// AddRemoteHold records that session holds the holding.
func (c *Cache) AddRemoteHold(ctx context.Context, key HolderKey, session string) (RemoteChange, error) {
	if session == "" {
		return RemoteChange{}, errEmptyRemoteHoldSession
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.addRemoteHoldLocked(key, session)
}

var errEmptyRemoteHoldSession = errors.New("add remote hold: empty session")

// addRemoteHoldLocked is AddRemoteHold's change, of a session that isn't
// empty. Requires egraphMu for writing.
func (c *Cache) addRemoteHoldLocked(key HolderKey, session string) (change RemoteChange, _ error) {
	defer c.takeChangesLocked(&change)
	_, h := c.holdingLocked(key)
	if h == nil {
		return change, fmt.Errorf("add remote hold: %w: %s/%d", ErrUnknownHolding, key.Cache, key.Number)
	}
	var added bool
	if h.sessions, added = withSession(h.sessions, session); added {
		c.markHoldingLocked(key)
	}
	state := c.remoteCacheLocked(key.Cache)
	held := state.sessions[session]
	if held == nil {
		held = make(map[uint64]struct{})
		state.sessions[session] = held
	}
	held[key.Number] = struct{}{}
	return change, nil
}

// ReleaseRemoteSession releases every hold of one session of an engine cache.
// The holdings it held are candidates for collection.
func (c *Cache) ReleaseRemoteSession(ctx context.Context, cache CacheID, session string) RemoteChange {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.releaseRemoteSessionChangeLocked(cache, session)
}

// releaseRemoteSessionChangeLocked is ReleaseRemoteSession's change.
// Requires egraphMu for writing.
func (c *Cache) releaseRemoteSessionChangeLocked(cache CacheID, session string) (change RemoteChange) {
	defer c.takeChangesLocked(&change)
	change.Candidates = c.releaseRemoteSessionLocked(cache, session, nil)
	return change
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
			var had bool
			if h.sessions, had = withoutSession(h.sessions, session); had {
				c.markHoldingLocked(key)
			}
			candidates = append(candidates, key)
		}
	}
	delete(state.sessions, session)
	return candidates
}

// ObserveRemoteRetention applies one observation of the holding's retention
// edge: the latest observation, by generation and then engine time, sets it.
// A holding left without retention is a candidate for collection.
func (c *Cache) ObserveRemoteRetention(ctx context.Context, key HolderKey, obs RetentionObservation) (RemoteChange, error) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.observeRemoteRetentionLocked(key, obs)
}

// observeRemoteRetentionLocked is ObserveRemoteRetention's change. Requires
// egraphMu for writing.
func (c *Cache) observeRemoteRetentionLocked(key HolderKey, obs RetentionObservation) (change RemoteChange, _ error) {
	defer c.takeChangesLocked(&change)
	_, h := c.holdingLocked(key)
	if h == nil {
		return change, fmt.Errorf("observe remote retention: %w: %s/%d", ErrUnknownHolding, key.Cache, key.Number)
	}
	before := h.retention
	h.retention.observe(obs.Retained, obs.ExpiresAtUnix, obsTime{generation: obs.Generation, engineTime: obs.EngineTimeUnixNano})
	if h.retention.retained != before.retained || h.retention.expiresAtUnix != before.expiresAtUnix {
		c.markHoldingLocked(key)
	}
	if !h.retention.retained {
		change.Candidates = []HolderKey{key}
	}
	return change, nil
}

// EndRemoteProcess ends the sessions of one process of an engine cache,
// releasing their holds. What its retention and dependencies own stays, under
// the same keys, for the cache's next process. The holdings the sessions held
// are candidates for collection.
func (c *Cache) EndRemoteProcess(ctx context.Context, cache CacheID, sessions []string) (change RemoteChange) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	defer c.takeChangesLocked(&change)
	for _, session := range sessions {
		change.Candidates = c.releaseRemoteSessionLocked(cache, session, change.Candidates)
	}
	return change
}

// ReleaseRemoteCache releases the holds and retention of every holding of an
// engine cache, and returns them all as candidates: collection removes them,
// through their dependencies on each other.
func (c *Cache) ReleaseRemoteCache(ctx context.Context, cache CacheID) RemoteChange {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.releaseRemoteCacheLocked(cache)
}

// releaseRemoteCacheLocked is ReleaseRemoteCache's change. Requires egraphMu
// for writing.
func (c *Cache) releaseRemoteCacheLocked(cache CacheID) (change RemoteChange) {
	defer c.takeChangesLocked(&change)
	state := c.remoteCaches[cache]
	if state == nil {
		return change
	}
	for number, id := range state.entries {
		key := HolderKey{Cache: cache, Number: number}
		h := c.resultsByID[id].holderLocked(key)
		if len(h.sessions) > 0 || h.retention.retained {
			c.markHoldingLocked(key)
		}
		h.sessions = nil
		h.retention = retentionRegister{}
		change.Candidates = append(change.Candidates, key)
	}
	clear(state.sessions)
	slices.SortFunc(change.Candidates, compareHolderKeys)
	return change
}

// CollectRemoteHoldings collects every candidate left with no session hold,
// no retention and no dependent, then the holdings only they owned, through
// holding dependencies. Each collected holding releases its entry's ownership
// unit, so entries left with no holding and no other owner are collected too.
// It returns the collected holdings' keys, sorted, and what it changed.
//
// It then compacts the e-graph's classes, at most once per
// eqClassCheckInterval and only when something changed since the last check
// (compactEqClassesAfterCollectionLocked), which frees the dead ones. Only the
// Cloud calls it, and never with snapshot sharing enabled, whose pending queue
// keeps class IDs. Engines compact through prune.
func (c *Cache) CollectRemoteHoldings(ctx context.Context, candidates []HolderKey) ([]HolderKey, RemoteChange, error) {
	c.egraphMu.Lock()
	collected, change, releases, err := c.collectRemoteHoldingsLocked(ctx, candidates)
	c.egraphMu.Unlock()
	slices.SortFunc(collected, compareHolderKeys)
	return collected, change, errors.Join(err, runOnReleaseFuncs(ctx, releases))
}

// collectRemoteHoldingsLocked is CollectRemoteHoldings' change. It returns
// the collected holdings unsorted, and what the collected entries release,
// to run once egraphMu is released. Requires egraphMu for writing.
func (c *Cache) collectRemoteHoldingsLocked(ctx context.Context, candidates []HolderKey) ([]HolderKey, RemoteChange, []OnReleaseFunc, error) {
	var change RemoteChange
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
	c.takeChangesLocked(&change)
	return collected, change, releases, errors.Join(rerr, err)
}

// eqClassSlotsLocked returns the class slots in use: class IDs start at 1,
// and the slots compaction freed below the highest live class hold none.
// Requires egraphMu.
func (c *Cache) eqClassSlotsLocked() int {
	if len(c.egraphParents) == 0 {
		return 0
	}
	return len(c.egraphParents) - 1 - len(c.freeEqClassIDs)
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

// CompactEqClasses frees, now, every class slot that no term and no entry
// uses: the forced form of the check CollectRemoteHoldings makes
// (compactEqClassesAfterCollectionLocked). It runs whatever the time since the
// last check, however few classes are dead, and frees every slot when no class
// is live. It returns the class slots before and after. It counts as that
// check, so the next collection checks again only after eqClassCheckInterval.
//
// Only a blob-backed cache answers, and never with snapshot sharing enabled,
// whose pending queue keeps class IDs: then, or when ctx is done before the
// compaction starts, it returns an error and changes nothing. A compaction
// that has started is not interrupted.
func (c *Cache) CompactEqClasses(ctx context.Context) (oldSlots, newSlots int, rerr error) {
	if !c.blobBacked {
		return 0, 0, errors.New("compact eq classes: the cache has no blob store")
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, fmt.Errorf("compact eq classes: %w", err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	// The context can end while another operation holds the graph.
	if err := ctx.Err(); err != nil {
		return 0, 0, fmt.Errorf("compact eq classes: %w", err)
	}
	if c.shareAdmission != snapshotShareOff {
		return 0, 0, errors.New("compact eq classes: snapshot sharing is enabled")
	}
	clock := c.eqClassClock
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	start := time.Now()
	c.eqClassChecks++
	compacted, oldSlots, newSlots := c.compactEqClassesModeLocked(true, true)
	c.eqClassCheckedAt = now
	c.eqClassCheckedSlots = c.eqClassSlotsLocked()
	c.eqClassRemoved = false
	slog.Debug("dagql collection checked eq classes",
		"compacted", compacted,
		"oldSlots", oldSlots,
		"newSlots", newSlots,
		"duration", time.Since(start),
		"forced", true)
	return oldSlots, newSlots, nil
}

// removeHoldingLocked takes a holding off its entry: out of its cache's index
// and count, releasing its ownership of its holding dependencies,
// whose keys it returns, and forgetting its unknown numbers. It releases the
// holding's unit on its entry, appending to entries those left to collect.
// Requires egraphMu for writing.
func (c *Cache) removeHoldingLocked(ctx context.Context, state *remoteCacheState, key HolderKey, entry *sharedResult, h *holding, entries []*sharedResult) ([]HolderKey, []*sharedResult, error) {
	// An entry left with no holding keeps none: a stored entry outlives
	// every holding of it.
	entry.deleteHolderLocked(key)
	state.graphVersion++
	c.markHoldingLocked(key)
	c.markEntryLocked(entry.id)
	delete(state.entries, key.Number)
	state.holdings--
	var deps []HolderKey
	for _, number := range h.deps {
		depKey := HolderKey{Cache: key.Cache, Number: number}
		if _, dep := c.holdingLocked(depKey); dep != nil {
			dep.dependents--
			c.markHoldingLocked(depKey)
			deps = append(deps, depKey)
		}
	}
	for _, number := range h.unknownDeps {
		state.forgetUnknownDependent(number, key.Number)
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
// (compactEqClassesAfterCollectionLocked). The candidates are change's.
func (c *Cache) CollectRemoteHolding(ctx context.Context, key HolderKey) (RemoteChange, bool, error) {
	var change RemoteChange
	c.egraphMu.Lock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		c.takeChangesLocked(&change)
		c.egraphMu.Unlock()
		return change, false, nil
	}
	entriesBefore := len(c.resultsByID)
	state := c.remoteCaches[key.Cache]
	if h.dependents > 0 {
		for number, id := range state.entries {
			parentKey := HolderKey{Cache: key.Cache, Number: number}
			parent := c.resultsByID[id].holderLocked(parentKey)
			var had bool
			if parent.deps, had = withoutNumber(parent.deps, key.Number); !had {
				continue
			}
			parent.unknownDeps = withNumber(parent.unknownDeps, key.Number)
			c.markHoldingLocked(parentKey)
			state.unknownDependents[key.Number] = insertNumber(state.unknownDependents[key.Number], parentKey.Number)
		}
	}
	for _, handle := range h.sessions {
		session := handleString(handle)
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
	c.takeChangesLocked(&change)
	c.egraphMu.Unlock()
	slices.SortFunc(candidates, compareHolderKeys)
	change.Candidates = candidates
	return change, true, errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases))
}

// ApplyHeldValueState applies one observation of an existing holding's value
// state, such as an offered reply's, by its replacement count
// (applyHeldValueStateLocked). It never creates a holding: an observation
// about one that doesn't exist is dropped, and reports false.
func (c *Cache) ApplyHeldValueState(ctx context.Context, key HolderKey, obs HeldValueState) (change RemoteChange, _ bool, _ error) {
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
	defer c.takeChangesLocked(&change)
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
	var known, unknown []uint64
	seen := make(map[uint64]struct{}, len(deps))
	for _, number := range deps {
		if _, ok := seen[number]; ok || number == key.Number || hasNumber(h.deps, number) || hasNumber(h.unknownDeps, number) {
			continue
		}
		seen[number] = struct{}{}
		if _, dep := c.holdingLocked(HolderKey{Cache: key.Cache, Number: number}); dep != nil {
			known = append(known, number)
			state.graphVersion++
			dep.dependents++
			c.markHoldingLocked(key)
			c.markHoldingLocked(HolderKey{Cache: key.Cache, Number: number})
			continue
		}
		unknown = append(unknown, number)
		c.markHoldingLocked(key)
		state.unknownDependents[number] = insertNumber(state.unknownDependents[number], key.Number)
	}
	h.deps = withNumbers(h.deps, known)
	h.unknownDeps = withNumbers(h.unknownDeps, unknown)
}

// forgetUnknownDependent removes parent from the holdings waiting for the
// holding number. Requires egraphMu for writing.
func (state *remoteCacheState) forgetUnknownDependent(number, parent uint64) {
	parents := removeNumber(state.unknownDependents[number], parent)
	if parents == nil {
		delete(state.unknownDependents, number)
		return
	}
	state.unknownDependents[number] = parents
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
	var parts []heldPart
	for _, key := range keys {
		i, found := partIndex(h.parts, key)
		if found && h.parts[i].state >= state {
			continue
		}
		if parts == nil {
			// One new slice for the whole observation.
			parts = slices.Clone(h.parts)
		}
		i, found = partIndex(parts, key)
		if found {
			if parts[i].state < state {
				parts[i].state = state
			}
			continue
		}
		parts = slices.Insert(parts, i, heldPart{key: internString(key), state: state})
	}
	if parts == nil {
		return false
	}
	h.parts = slices.Clip(parts)
	return true
}

// teachRemoteIdentityLocked makes the entry an output of the class of its
// recipe, the request digest and the content digest, and applies the term to
// that class, as a publication indexes a computed result. Most rows report an
// identity the cache already holds, as a repeat hit does locally: then
// nothing would change, and nothing is done. Requires egraphMu for writing.
func (c *Cache) teachRemoteIdentityLocked(ctx context.Context, entry *sharedResult, field string, recipe, request, content digest.Digest, term *RemoteTerm) {
	// The entry's own frame takes its content digest, as the engine's did
	// when it was taught it, whether or not the e-graph learns anything: a
	// record sent from the Cloud then carries it, and a fresh engine joins
	// its class as the engine that computed it does. A digest taught again
	// after another is the frame's content again, as on the engine.
	if content != "" && entry != nil && entry.id != 0 {
		if n := len(entry.taught); n == 0 || entry.taught[n-1] != content {
			entry.taught = append(entry.taught, content)
		}
		c.queueTaughtContentLocked(entry)
	}
	if c.remoteIdentityTeachesNothingLocked(entry, recipe, request, content, term) {
		return
	}
	c.writeRemoteIdentityLocked(ctx, entry, field, recipe, request, content, term)
}

// remoteIdentityTeachesNothingLocked reports whether writeRemoteIdentityLocked
// would leave every structure as it is: each digest already in the entry's
// single output class and posting the entry, the entry a member of that class
// in both directions, the content digest already a fact of the class, the
// entry already associated with the term, and every term of that digest
// already pointing at the class. It is hitTeachesNothingLocked for a remote
// identity. It only reads, finding roots without path compression, and answers
// false whenever any step would write. Congruence repair after a merge that
// unions nothing finds nothing to repair: every union repairs to closure
// before mergeEqClassesLocked returns. Requires egraphMu, for reading or
// writing.
func (c *Cache) remoteIdentityTeachesNothingLocked(entry *sharedResult, recipe, request, content digest.Digest, term *RemoteTerm) bool {
	if entry == nil || entry.id == 0 {
		return false
	}
	// The class the teach would merge the entry's output classes into, which
	// addResultOutputEqClassLocked records in both directions.
	members := c.resultOutputEqClasses[entry.id]
	var root eqClassID
	for id := range members {
		r := c.eqClassRootLocked(id)
		if r == 0 {
			continue
		}
		if root != 0 && r != root {
			return false
		}
		root = r
	}
	if root == 0 {
		return false
	}
	if _, ok := members[root]; !ok {
		return false
	}
	if _, ok := c.outputEqClassResults[root][entry.id]; !ok {
		return false
	}
	for _, dig := range []digest.Digest{recipe, request, content} {
		if dig == "" {
			continue
		}
		id, ok := c.egraphDigestToClass[dig.String()]
		if !ok || c.eqClassRootLocked(id) != root {
			return false
		}
		posting := c.egraphResultsByDigest[dig.String()]
		if posting == nil || !posting.Contains(entry.id) {
			return false
		}
	}
	if content != "" {
		if _, ok := c.eqClassExtraDigests[root][call.ExtraDigest{Digest: content, Label: call.ExtraDigestLabelContent}]; !ok {
			return false
		}
	}
	if term == nil || term.Self == "" {
		return true
	}
	// applyPreparedResultIdentityLocked: the term's inputs already have
	// classes, the entry already has a live term of the digest they give, and
	// mergeOutputsForTermDigestLocked would store no new root on a term.
	inputEqIDs := make([]eqClassID, len(term.Inputs))
	for i, in := range term.Inputs {
		if in == "" {
			continue
		}
		id, ok := c.egraphDigestToClass[in.String()]
		if !ok {
			return false
		}
		inputEqIDs[i] = c.eqClassRootLocked(id)
	}
	termDigest := calcEgraphTermDigest(term.Self, inputEqIDs)
	if c.termForResultByDigestLocked(entry.id, termDigest) == nil {
		return false
	}
	for termID := range c.egraphTermsByTermDigest[termDigest].Items() {
		if t := c.egraphTerms[termID]; t != nil && t.outputEqID != root {
			return false
		}
	}
	return true
}

// writeRemoteIdentityLocked is teachRemoteIdentityLocked's write. Requires
// egraphMu for writing.
func (c *Cache) writeRemoteIdentityLocked(ctx context.Context, entry *sharedResult, field string, recipe, request, content digest.Digest, term *RemoteTerm) {
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
		c.addResultDigestPostingLocked(entry.id, dig.String())
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
			for _, ref := range res.holderRefsLocked() {
				keys = append(keys, ref.key)
			}
		}
	}
	slices.SortFunc(keys, compareHolderKeys)
	return keys
}

// HolderClosure returns the holding and every holding it depends on,
// transitively, through its cache's own dependencies: what that engine cache
// exports for it. It is empty when the cache has no such holding.
//
// It reads the closure in pieces (readInPieces), letting egraphMu go between
// them, so that a writer waits for one piece rather than a whole closure: a
// test-base engine's closures reach tens of thousands of holdings, which held
// the lock up to seconds. The answer is still one state's: a walk whose
// cache's holdings or dependencies changed between two of its pieces
// (graphVersion) starts again, and after holderClosureTries such walks it
// reads the closure in one hold.
func (c *Cache) HolderClosure(key HolderKey) []HolderKey {
	for range holderClosureTries {
		if keys, ok := c.holderClosureInPieces(key); ok {
			return keys
		}
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.holderClosureLocked(key)
}

// holderClosureTries is how many walks in pieces HolderClosure starts before
// it reads the closure in one hold.
const holderClosureTries = 3

// holderClosureInPieces is HolderClosure's walk in pieces. It reports false
// when the cache's holdings changed between two pieces: its keys would mix
// two states.
func (c *Cache) holderClosureInPieces(key HolderKey) ([]HolderKey, bool) {
	r := c.readInPieces()
	defer r.done()
	state := c.remoteCaches[key.Cache]
	if _, h := c.holdingLocked(key); h == nil || state == nil {
		return nil, true
	}
	version := state.graphVersion
	seen := map[uint64]bool{key.Number: true}
	queue := []uint64{key.Number}
	var keys []HolderKey
	for len(queue) > 0 {
		number := queue[0]
		queue = queue[1:]
		if _, h := c.holdingLocked(HolderKey{Cache: key.Cache, Number: number}); h != nil {
			keys = append(keys, HolderKey{Cache: key.Cache, Number: number})
			for _, dep := range h.deps {
				if !seen[dep] {
					seen[dep] = true
					queue = append(queue, dep)
				}
			}
		}
		if len(queue) > 0 && r.step() && (c.remoteCaches[key.Cache] != state || state.graphVersion != version) {
			return nil, false
		}
	}
	slices.SortFunc(keys, compareHolderKeys)
	return keys, true
}

// holderClosureLocked is HolderClosure's answer. Requires egraphMu.
func (c *Cache) holderClosureLocked(key HolderKey) []HolderKey {
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
		for _, dep := range h.deps {
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
	// Executed reports that the holding's cache computed the entry since
	// the cache last dropped the entry's value (RemoteHolding.Executed).
	Executed bool
	// Image reports that the holding's cache has the entry from its engine
	// image (RemoteHolding.Image).
	Image bool
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

// RemoteHeldEntry is what RemoteEntryInfo reports for one holding and its
// entry's own facts, without the entry's other holdings, digests, classes or
// terms.
type RemoteHeldEntry struct {
	// Number, Stored, StoredExpiresAtUnix and Recipes are the entry's, as
	// RemoteEntryInfo reports them.
	Number              uint64
	Stored              bool
	StoredExpiresAtUnix int64
	Recipes             []digest.Digest
	// Holding is the holding's description, as in RemoteEntryInfo.Holdings.
	Holding RemoteHoldingInfo
}

// RemoteHoldingInfo returns what RemoteEntryInfo reports for the holding key
// and its entry's own facts, read under one hold. It describes no other
// holding and none of the entry's classes, so its cost doesn't grow with the
// entry's holders or classes; it still clones and sorts all of the entry's
// recipes, and grows with those.
func (c *Cache) RemoteHoldingInfo(key HolderKey) (RemoteHeldEntry, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.remoteHoldingInfoLocked(key)
}

// remoteHoldingInfoLocked is RemoteHoldingInfo's answer. Requires egraphMu.
func (c *Cache) remoteHoldingInfoLocked(key HolderKey) (RemoteHeldEntry, bool) {
	entry, h := c.holdingLocked(key)
	if h == nil {
		return RemoteHeldEntry{}, false
	}
	held := RemoteHeldEntry{Holding: holdingInfo(key, h)}
	held.Number, held.Stored, held.StoredExpiresAtUnix, held.Recipes = entryFactsLocked(entry)
	return held, true
}

// HolderEntry returns the number of the entry a holding sits on, as
// RemoteHoldingInfo reports it, and whether the holding exists, without
// describing the holding or the entry: for a caller that needs only which
// entry a holding is on.
func (c *Cache) HolderEntry(key HolderKey) (uint64, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.holderEntryLocked(key)
}

// holderEntryLocked is HolderEntry's answer. Requires egraphMu.
func (c *Cache) holderEntryLocked(key HolderKey) (uint64, bool) {
	entry, h := c.holdingLocked(key)
	if h == nil {
		return 0, false
	}
	return uint64(entry.id), true
}

// EntryClass is an entry's own facts, as RemoteEntryInfo reports them, and
// the entries of its classes, itself among them, without any holding.
type EntryClass struct {
	Number              uint64
	Stored              bool
	StoredExpiresAtUnix int64
	// Entries are the numbers of the entries in the entry's output
	// equivalence classes, the entry's own included, sorted: those
	// EquivalentEntries returns for each of its class digests.
	Entries []uint64
}

// EntryClassEntries returns the entry numbered number's facts and its
// classes' entries, read under one hold. Unlike EntryInfo, it describes no
// holding, digest or term, so its cost grows with the classes' entries, not
// with how many engine caches hold them. It reports false when the cache has
// no such entry.
func (c *Cache) EntryClassEntries(number uint64) (EntryClass, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.entryClassEntriesLocked(number)
}

// entryClassEntriesLocked is EntryClassEntries' answer. Requires egraphMu.
func (c *Cache) entryClassEntriesLocked(number uint64) (EntryClass, bool) {
	entry := c.resultsByID[sharedResultID(number)]
	if entry == nil {
		return EntryClass{}, false
	}
	var class EntryClass
	class.Number, class.Stored, class.StoredExpiresAtUnix, _ = entryFactsLocked(entry)
	entries := map[sharedResultID]struct{}{entry.id: {}}
	for root := range c.outputEqClassRootsLocked(entry.id) {
		for id := range c.outputEqClassResults[root] {
			if c.resultsByID[id] != nil {
				entries[id] = struct{}{}
			}
		}
	}
	for id := range entries {
		class.Entries = append(class.Entries, uint64(id))
	}
	slices.Sort(class.Entries)
	return class, true
}

// CacheHoldings returns the holdings of one engine cache on the entries
// numbered numbers, each as RemoteHoldingInfo describes it with its entry's
// facts, in the order of numbers and then by holding number, read under one
// hold; an entry's holdings share its Recipes slice. Its cost grows with the
// entries and that cache's holdings on them, not with other caches'
// (cacheHolderRefsLocked). An entry the cache doesn't have, or one the engine cache
// holds nothing on, is skipped.
func (c *Cache) CacheHoldings(cache CacheID, numbers []uint64) []RemoteHeldEntry {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var out []RemoteHeldEntry
	for _, number := range numbers {
		entry := c.resultsByID[sharedResultID(number)]
		if entry == nil {
			continue
		}
		refs := entry.cacheHolderRefsLocked(cache)
		if len(refs) == 0 {
			continue
		}
		var held RemoteHeldEntry
		held.Number, held.Stored, held.StoredExpiresAtUnix, held.Recipes = entryFactsLocked(entry)
		for _, ref := range refs {
			held.Holding = holdingInfo(ref.key, ref.h)
			out = append(out, held)
		}
	}
	return out
}

// EntryHeld is an entry's own facts, as RemoteEntryInfo reports them, and the
// keys of its first holdings, without describing any holding.
type EntryHeld struct {
	Number              uint64
	Stored              bool
	StoredExpiresAtUnix int64
	// Keys are the keys of the entry's first holdings, as many as asked, in
	// RemoteEntryInfo.Holdings' order.
	Keys []HolderKey
}

// EntriesHeld returns, for each of numbers the cache has an entry for, in
// their order, the entry's facts and the keys of its first limit holdings
// (none for limit 0), read under one hold. A number the cache has no entry
// for is skipped: a caller matches the results by Number. Unlike EntryInfo,
// it describes no holding, digest or term, so its cost grows with the
// entries, and with their holders only when it lists their keys.
func (c *Cache) EntriesHeld(numbers []uint64, limit int) []EntryHeld {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	out := make([]EntryHeld, 0, len(numbers))
	for _, number := range numbers {
		entry := c.resultsByID[sharedResultID(number)]
		if entry == nil {
			continue
		}
		held := EntryHeld{Number: uint64(entry.id)}
		if !entry.noValueLocked() {
			held.Stored, held.StoredExpiresAtUnix = true, entry.expiresAtUnix
		}
		// The holdings are sorted by key, RemoteEntryInfo.Holdings' order:
		// the first limit are a prefix.
		if refs := entry.holderRefsLocked(); limit > 0 && len(refs) > 0 {
			held.Keys = make([]HolderKey, 0, min(limit, len(refs)))
			for _, ref := range refs[:min(limit, len(refs))] {
				held.Keys = append(held.Keys, ref.key)
			}
		}
		out = append(out, held)
	}
	return out
}

// entryFactsLocked returns entry's number, whether it stores a record and
// that record's expiry, and its recipes, sorted. Requires egraphMu.
func entryFactsLocked(entry *sharedResult) (number uint64, stored bool, storedExpiresAtUnix int64, recipes []digest.Digest) {
	if !entry.noValueLocked() {
		stored, storedExpiresAtUnix = true, entry.expiresAtUnix
	}
	recipes = slices.Clone(entry.recipeKeys)
	slices.Sort(recipes)
	return uint64(entry.id), stored, storedExpiresAtUnix, recipes
}

// remoteEntryInfoLocked describes entry: its number, whether it stores a
// record and that record's expiry, its identity and its holdings. Requires
// egraphMu.
func (c *Cache) remoteEntryInfoLocked(entry *sharedResult) RemoteEntryInfo {
	info := RemoteEntryInfo{Field: entry.description}
	info.Number, info.Stored, info.StoredExpiresAtUnix, info.Recipes = entryFactsLocked(entry)
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
	for _, ref := range entry.holderRefsLocked() {
		info.Holdings = append(info.Holdings, holdingInfo(ref.key, ref.h))
	}
	return info
}

func holdingInfo(key HolderKey, h *holding) RemoteHoldingInfo {
	info := RemoteHoldingInfo{
		Key:           key,
		TypeName:      handleString(h.typeName),
		ContentDigest: digest.Digest(handleString(h.contentDigest)),
		ExpiresAtUnix: h.expiresAtUnix,
		Retained:      h.retention.retained,
		Dependents:    int(h.dependents),
		Replacements:  h.replacements,
		Executed:      h.executed,
		Image:         h.image,
		Deps:          slices.Clone(h.deps),
		UnknownDeps:   slices.Clone(h.unknownDeps),
	}
	if h.retention.retained {
		info.RetentionExpiresAtUnix = h.retention.expiresAtUnix
	}
	for _, session := range h.sessions {
		info.Sessions = append(info.Sessions, handleString(session))
	}
	info.Parts, info.OfferedParts = heldPartAddresses(h.parts, true)
	return info
}

// heldPartAddresses parses the parts' address keys, in key order: the
// complete parts, and, with offered, the parts held only as offers.
func heldPartAddresses(parts []heldPart, offered bool) (complete, offers []PersistedPartAddress) {
	for _, part := range parts {
		if part.state != heldPartComplete && !offered {
			continue
		}
		var address PersistedPartAddress
		if err := json.Unmarshal([]byte(handleString(part.key)), &address); err != nil {
			continue
		}
		if part.state == heldPartComplete {
			complete = append(complete, address)
		} else {
			offers = append(offers, address)
		}
	}
	return complete, offers
}

// HoldingFacts is what a selection pass reads of one holding and its entry:
// RemoteHoldingInfo's fields of the same names, and its entry's own facts,
// without its sessions, offered parts, content digest, dependents or
// recipes. Deps and UnknownDeps are the holding's own slices, which a write
// replaces and never changes: they stay as read, and the caller must not
// change them.
type HoldingFacts struct {
	Number                 uint64
	Stored                 bool
	StoredExpiresAtUnix    int64
	TypeName               string
	ExpiresAtUnix          int64
	Retained               bool
	RetentionExpiresAtUnix int64
	Replacements           uint64
	Executed               bool
	Image                  bool
	Deps                   []uint64
	UnknownDeps            []uint64
	// Parts are the copy's complete parts, as RemoteHoldingInfo.Parts.
	Parts []PersistedPartAddress
}

// HoldingFacts returns the facts of the holding key and its entry, read under
// one hold, without copying its dependencies. It reports false when the
// cache has no such holding.
func (c *Cache) HoldingFacts(key HolderKey) (HoldingFacts, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	entry, h := c.holdingLocked(key)
	if h == nil {
		return HoldingFacts{}, false
	}
	facts := HoldingFacts{
		Number:        uint64(entry.id),
		TypeName:      handleString(h.typeName),
		ExpiresAtUnix: h.expiresAtUnix,
		Retained:      h.retention.retained,
		Replacements:  h.replacements,
		Executed:      h.executed,
		Image:         h.image,
		Deps:          h.deps,
		UnknownDeps:   h.unknownDeps,
	}
	if !entry.noValueLocked() {
		facts.Stored, facts.StoredExpiresAtUnix = true, entry.expiresAtUnix
	}
	if h.retention.retained {
		facts.RetentionExpiresAtUnix = h.retention.expiresAtUnix
	}
	facts.Parts, _ = heldPartAddresses(h.parts, false)
	return facts, true
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
