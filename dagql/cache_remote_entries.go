package dagql

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
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
	// Executed reports that the call computed the entry: it executed or
	// joined the call, as the service counts executed evidence.
	Executed bool
	// FollowsFromInputs is the call's declaration that its result follows
	// from its inputs (ResultCall.FollowsFromInputs), as its row stamps it
	// (dagger.io/cache.follows_from_inputs). It says so only with CallRow.
	FollowsFromInputs bool
	// CallRow reports that the description is a call row's, which stamps
	// the declaration, unlike one a caller makes of a record it holds.
	CallRow bool
	// BeforeLastDrop reports that the call's row reached the Cloud before
	// its latest drop of values. Once the cache has dropped a value, only a
	// computation after the drop may bring it back (holding.executed), so
	// such a row's computation doesn't, though it still says the run ran
	// (holding.ran).
	BeforeLastDrop bool
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
	// Evaluated reports that the update is an evaluation of the value by
	// the holding's cache: a lazy span that didn't install its parts from a
	// Cloud offer. Its complete parts then say the cache ran the value
	// (holding.ran). A part shared from another entry, or installed from an
	// offer, says nothing of it.
	Evaluated bool
}

// RemoteChange is what one operation on the Cloud cache's holdings changed,
// for the service to act on. Candidates are the holdings it created or
// released an owner of, for the next CollectRemoteHoldings: no operation
// collects by itself.
//
// On a blob-backed cache, Holdings and Entries are what a reader that keeps
// its answers asks again (cache_change_marks.go), sorted, holdings by number
// and then cache:
//   - Holdings: every holding whose facts (HoldingFacts, what a reader may
//     keep of its description) may differ after the operation, those it
//     created or removed included. The rest of a description, its content
//     digest, the sessions holding it, its count of dependents and its
//     offered parts, a reader reads fresh: a change to them alone names no
//     holding;
//   - Entries: every entry of which any of these may differ: whether it
//     exists; its own facts, Stored, StoredExpiresAtUnix and Recipes; and
//     any AvailablePart answer at one clock. A holding's description doesn't
//     change with its entry: a holding whose description changes is in
//     Holdings, whatever entries are named.
//
// Neither covers the entries of an entry's classes (EntryClassEntries),
// which a member joining or leaving changes for every class mate, nor the
// keys of an entry's holdings (EntriesHeld, EntryInfo), which a holding
// arriving or leaving changes: no reader keeps either from one change to the
// next, and a holding's arrival or removal is in Holdings. An entry's facts
// and part answers read its holders only for whether any engine holds it
// (noValueLocked), which turns only as the entry is created or leaves, each
// of which names it. Nor do they cover the digests, class digests, terms or
// field of an entry, which only EntryInfo returns.
type RemoteChange struct {
	Candidates []HolderKey
	Holdings   []HolderKey
	Entries    []uint64
	// Saved reports that the operation changed what a save of the cache
	// writes, beyond the holdings, which aren't saved: a call kept on a
	// stored entry, the entry's expiry, or that its stored record's run ran.
	Saved bool
	// Shared are the kinds of state engines share that the operation
	// changed, on a blob-backed cache (SharedWrites): none when it changed
	// only its engine caches' own.
	Shared SharedWrites
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
	// cloudMerge is, on an engine's Cloud holding, what the engine last
	// merged of the Cloud entry's record (cloudMergeNote): nil until a merge
	// notes it, and on every other holding.
	cloudMerge *cloudMergeNote
	// executed marks a holding whose cache computed the entry: a call span
	// with executed evidence, which reached the Cloud after its latest drop
	// (RemoteHolding.BeforeLastDrop), named it. A prune that drops the entry's value
	// clears it on every holding of the entry, so the service takes a
	// dropped value back only from a cache that computed it after the drop;
	// copies from placement, and entries a cache only hit, are never
	// flagged.
	executed bool
	// ran marks a holding whose cache ran its value at its replacement
	// count: a call span with executed or joined evidence, or a lazy span
	// that didn't install it from a Cloud offer, reported a part besides its
	// metadata complete at that count. A higher count clears it. Unlike executed, a prune leaves it, and nothing decides by
	// it: it tells whether the run a stored record names ran
	// (noteHoldingRanLocked).
	ran bool
	// served is, on the Cloud, the run of the record the Cloud last served
	// the holding's cache for this entry (ApplyMergedReply), at the
	// replacement count the serve describes: a copy the engine exports
	// without running it is that run (mergeRunLocked). A later serve
	// replaces it, and a higher count drops it, so a holding keeps at most
	// one; it goes with the holding.
	served *servedRun
	// follows is the declaration the holding's latest call row stamped, that
	// the entry's result follows from its inputs
	// (RemoteHolding.FollowsFromInputs): one of the two places the Cloud
	// keeps it (entryCanDifferLocked). rowed marks a holding a call row has
	// reported, whose follows says so: a merge's holding of its sender has
	// none.
	follows, rowed bool
}

// servedRun is the run of a record the Cloud served an engine cache, at
// the engine entry's replacement count, with what the Cloud knew then of
// whether it ran.
type servedRun struct {
	run          RunName
	ran          runRanBy
	replacements uint64
	// known is false for a serve whose record named no run, or whose run
	// the reply's bundle doesn't carry: the copy's run isn't known, and no
	// other run is taken for it (mergeRunLocked).
	known bool
}

// hasUnexpiredHoldingLocked reports whether another cache holds a copy of res
// that has a value and has not expired: a holding whose own expiry is unset
// or still ahead. It reads every engine cache's holding of res, and takes
// none of their locks: it requires egraphMu for writing, as an entry's
// removal holds it.
func (res *sharedResult) hasUnexpiredHoldingLocked(nowUnix int64) bool {
	unexpired := false
	res.eachHolderLocked(func(ref holderRef) bool {
		h := ref.h
		if !h.unstored && (h.expiresAtUnix == 0 || nowUnix < h.expiresAtUnix) {
			unexpired = true
		}
		return !unexpired
	})
	return unexpired
}

func (h *holding) owned() bool {
	return len(h.sessions) > 0 || h.retention.retained || h.dependents > 0
}

// remoteCacheState is what the cache keeps for one held engine cache beside
// its holdings. Guarded by egraphMu.
type remoteCacheState struct {
	// mu is the engine cache's own lock. It guards this state and the
	// fields of its holdings (holding) against the cache's readers once
	// writes of this engine cache's holdings hold egraphMu for reading
	// beside other engines' writes: a writer of its holdings takes it for one
	// operation at a time. Until then every write holds egraphMu exclusively
	// and takes no engine cache's lock, as it excludes every reader. A
	// reader takes it for reading at the reader's own entry, never inside a
	// helper a write also calls: once a piece for a walk within one engine
	// cache (readPieces.lockEngine), once a holding for a walk across
	// engine caches. It is only ever taken under egraphMu, and a holder of
	// one engine cache's lock takes no other's; an entry's holder list's
	// lock (entryHolders) comes after it.
	mu       sync.RWMutex
	holdings int
	// graphVersion counts the changes to the cache's holdings and their
	// dependencies: a holding added or removed, a dependency added or
	// dropped. HolderClosure reads a closure in pieces, and starts again
	// when it changed between two of them.
	graphVersion uint64
	// entries maps each holding's number to the holding and the entry it
	// sits on: finding a holding is one access, however many caches hold its
	// entry.
	entries map[uint64]heldEntry
	// unknownDependents maps each dependency number no holding of this cache
	// has yet to the numbers of the holdings that name it, sorted.
	unknownDependents map[uint64][]uint64
	// sessions maps each session to the numbers of the holdings it holds.
	sessions map[string]map[uint64]struct{}
}

// heldEntry is one holding of an engine cache with the entry it sits on, as
// the cache's index keeps it (remoteCacheState.entries). It is the same
// holding as the entry's holders name under the holding's key: a holding is
// added to both and removed from both together (addHoldingLocked,
// removeHoldingLocked), and an entry leaves the cache only once no holding
// owns it.
type heldEntry struct {
	entry *sharedResult
	h     *holding
}

// holdingLocked returns the holding numbered number of the cache and the
// entry it sits on, or nils. Requires egraphMu.
func (state *remoteCacheState) holdingLocked(number uint64) (*sharedResult, *holding) {
	held := state.entries[number]
	return held.entry, held.h
}

// noValueLocked reports an entry known only through holdings: it has no value
// of its own, neither decoded nor stored, so it is never served, loaded or
// selected. A holding of an engine cache tells it from a cached nil result,
// which also has neither but is served, even when the Cloud holds it too.
// Requires egraphMu.
func (res *sharedResult) noValueLocked() bool {
	if res.engineHoldersLocked() == 0 {
		return false
	}
	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	return !res.hasValue && res.persistedEnvelope == nil
}

// rlockEngineLocked takes the lock of the engine cache id's state for
// reading, and returns the state; nil, with nothing locked, when the cache
// has none. The reader lets it go (runlock) before it takes another engine
// cache's lock. Requires egraphMu for reading, and no engine cache's lock.
func (c *Cache) rlockEngineLocked(id CacheID) *remoteCacheState {
	state := c.remoteCaches[id]
	if state != nil {
		state.mu.RLock()
	}
	return state
}

// runlock lets go of the lock rlockEngineLocked took; nothing for nil.
func (state *remoteCacheState) runlock() {
	if state != nil {
		state.mu.RUnlock()
	}
}

// holdingLocked returns the holding key and the entry it sits on, or nils,
// from its engine cache's index. Requires egraphMu.
func (c *Cache) holdingLocked(key HolderKey) (*sharedResult, *holding) {
	state := c.remoteCaches[key.Cache]
	if state == nil {
		return nil, nil
	}
	return state.holdingLocked(key.Number)
}

// remoteCacheLocked returns the state of one held engine cache, creating it
// for its first holding (addHoldingLocked): a state exists while its cache
// has a holding. Requires egraphMu for writing.
func (c *Cache) remoteCacheLocked(change *RemoteChange, id CacheID) *remoteCacheState {
	if c.remoteCaches == nil {
		c.remoteCaches = make(map[CacheID]*remoteCacheState)
	}
	state := c.remoteCaches[id]
	if state == nil {
		state = &remoteCacheState{
			entries:           make(map[uint64]heldEntry),
			unknownDependents: make(map[uint64][]uint64),
			sessions:          make(map[string]map[uint64]struct{}),
		}
		c.remoteCaches[id] = state
		c.noteSharedWriteLocked(change, SharedNewEngineCache)
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
	return c.AttachPreparedRemoteHolding(ctx, key, desc, PrepareRemoteHolding(key, desc))
}

// AttachPreparedRemoteHolding is AttachRemoteHolding, with key and desc
// prepared before (PrepareRemoteHolding).
func (c *Cache) AttachPreparedRemoteHolding(ctx context.Context, key HolderKey, desc RemoteHolding, prepared PreparedRemoteHolding) (RemoteChange, error) {
	if prepared.err != nil {
		return RemoteChange{}, prepared.err
	}
	c.lockGraphFor(ctx, workRemote)
	defer c.egraphMu.Unlock()
	return c.attachRemoteHoldingLocked(ctx, key, desc, prepared.parts, prepared.kept), nil
}

// PreparedRemoteHolding is what attaching a holding reads of its key and
// description without the cache (PrepareRemoteHolding): its parts' keys and
// the call it keeps, or why it can't attach. A holder that prepares it before
// it takes the cache's lock, as PrepareMergedReply prepares a merged reply,
// spends none of that in its hold.
type PreparedRemoteHolding struct {
	parts []string
	kept  *keptCall
	err   error
}

// PrepareRemoteHolding prepares the holding of key that desc describes, for
// AttachPreparedRemoteHolding of the same key and description. It reads
// nothing of the cache, and of desc only its recipe, request, call and parts.
func PrepareRemoteHolding(key HolderKey, desc RemoteHolding) PreparedRemoteHolding {
	parts, kept, err := prepareRemoteHolding(key, desc)
	return PreparedRemoteHolding{parts: parts, kept: kept, err: err}
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
	entry, h := c.holdingLocked(key)
	if h == nil {
		entry, h = c.newHoldingLocked(ctx, &change, key, desc)
		change.Candidates = []HolderKey{key}
	}
	state := c.remoteCaches[key.Cache]
	c.teachRemoteIdentityLocked(ctx, &change, entry, desc.Field, desc.Recipe, desc.Request, desc.ContentDigest, desc.Term)
	change.Saved = c.keepCallLocked(&change, entry, desc, kept)
	if desc.TypeName != "" && desc.TypeName != handleString(h.typeName) {
		h.typeName = internString(desc.TypeName)
		c.markHoldingLocked(&change, key)
	}
	if desc.Executed && !desc.BeforeLastDrop && !h.executed {
		h.executed = true
		c.markHoldingLocked(&change, key)
	}
	if desc.CallRow && (!h.rowed || desc.FollowsFromInputs != h.follows) {
		h.follows, h.rowed = desc.FollowsFromInputs, true
		c.markHoldingLocked(&change, key)
	}
	touchSharedResultLastUsed(entry, desc.UsedAtUnixNano)
	candidates := c.applyHeldValueStateLocked(ctx, &change, state, key, entry, h, HeldValueState{
		Replacements:  desc.Replacements,
		Deps:          desc.Deps,
		ExpiresAtUnix: desc.ExpiresAtUnix,
		ContentDigest: desc.ContentDigest,
	}, parts, nil)
	change.Candidates = append(change.Candidates, candidates...)
	// An executed or joined call that reported a part besides its metadata
	// complete ran the value at its count. A call that returned a lazy
	// result reports its parts pending: its step runs later, if at all, and
	// its evaluation's row says so.
	if desc.Executed && ranPartsReported(parts) && c.noteHoldingRanLocked(&change, key, entry, h, desc.Replacements, runRanByCall) {
		change.Saved = true
	}
	return change
}

// newHoldingLocked attaches a new holding to the entry of desc's recipe and
// gives it its waiting dependents. Requires egraphMu for writing.
func (c *Cache) newHoldingLocked(ctx context.Context, change *RemoteChange, key HolderKey, desc RemoteHolding) (*sharedResult, *holding) {
	entry := c.resultsByID[c.entriesByRecipe[desc.Recipe]]
	if entry == nil {
		entry = &sharedResult{
			id:          c.takeResultIDs(1),
			description: desc.Field,
		}
		c.putResultLocked(entry)
		c.indexRecipeLocked(desc.Recipe, entry)
		c.noteSharedWriteLocked(change, SharedNewEntry)
	}
	return entry, c.addHoldingLocked(ctx, change, key, entry)
}

// addHoldingLocked attaches a new holding to entry and gives it its waiting
// dependents: holdings of its cache that named its number before it existed.
// Requires egraphMu for writing.
func (c *Cache) addHoldingLocked(ctx context.Context, change *RemoteChange, key HolderKey, entry *sharedResult) *holding {
	// An engine cache's state exists while it has a holding: it is created
	// with the first, and goes with the last (collectRemoteHoldingsLocked,
	// removeGoneHoldingLocked).
	state := c.remoteCacheLocked(change, key.Cache)
	h := &holding{}
	state.graphVersion++
	entry.putHolderLocked(key, h)
	// The holding is marked, not its entry: no reader keeps an entry's
	// holders (RemoteChange), and its own facts and part answers don't read
	// them, but for whether any engine holds it, which a holding's arrival
	// turns only as it creates the entry, whose creation marks it.
	c.markHoldingLocked(change, key)
	state.entries[key.Number] = heldEntry{entry: entry, h: h}
	// Each holding is one ownership unit of its entry.
	c.incrementIncomingOwnershipLocked(ctx, entry)
	state.holdings++
	c.remoteHoldings.Add(1)
	for _, parent := range state.unknownDependents[key.Number] {
		if _, parentHolding := state.holdingLocked(parent); parentHolding != nil {
			parentHolding.unknownDeps, _ = withoutNumber(parentHolding.unknownDeps, key.Number)
			parentHolding.deps = withNumber(parentHolding.deps, key.Number)
			c.markHoldingLocked(change, HolderKey{Cache: key.Cache, Number: parent})
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
	c.lockGraphFor(ctx, workRemote)
	defer c.egraphMu.Unlock()
	return c.applyMergedReplyLocked(ctx, cache, cloudEntries, targets, reply), nil
}

// mergedTarget is a record of a merged reply, with its complete and offered
// part address keys.
type mergedTarget struct {
	value         MergedValue
	parts, offers []string
	// served is the run the sent record named when the bundle was built
	// (TransferredValue.served), nil when it named none or the bundle
	// doesn't carry it.
	served *servedRun
}

// prepareMergedReply checks a merged reply against the bundle sent, and
// returns the Cloud entry of each of the bundle's ordinals and the reply's
// records, ready to apply. It reads nothing of the cache.
func prepareMergedReply(cache CacheID, sent ValueBundle, reply MergeReply) (map[TransferOrdinal]sharedResultID, []mergedTarget, error) {
	if cache == "" || senderCache(cache) {
		return nil, nil, fmt.Errorf("apply merged reply: %q is not an engine cache", cache)
	}
	cloudEntries := make(map[TransferOrdinal]sharedResultID, len(sent.Values))
	served := make(map[TransferOrdinal]*servedRun, len(sent.Values))
	for _, value := range sent.Values {
		cloudEntries[value.Ordinal] = sharedResultID(value.SenderNumber)
		served[value.Ordinal] = value.served
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
		targets = append(targets, mergedTarget{value: value, parts: parts, offers: offers, served: served[value.Ordinal]})
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
	for _, t := range targets {
		key := HolderKey{Cache: cache, Number: t.value.Number}
		entry, h := c.holdingLocked(key)
		if h == nil {
			entry = c.resultsByID[cloudEntries[t.value.Ordinal]]
			if entry == nil {
				continue
			}
			h = c.addHoldingLocked(ctx, change, key, entry)
			change.Candidates = append(change.Candidates, key)
		}
		state := c.remoteCaches[cache]
		before := h.replacements
		candidates := c.applyHeldValueStateLocked(ctx, change, state, key, entry, h, HeldValueState{
			Replacements:  t.value.Replacements,
			Deps:          t.value.Deps,
			ExpiresAtUnix: t.value.ExpiresAtUnix,
		}, t.parts, t.offers)
		change.Candidates = append(change.Candidates, candidates...)
		// The engine's copy is the run of the record the Cloud served it,
		// which may be another entry's than the one its holding is on: a
		// class mate's, when the engine's call joined it by content. The
		// run is the one the sent record named when the bundle was built,
		// whatever the entry holds now; one the bundle doesn't carry is not
		// known, and stands as that. A value the engine kept rather than
		// stored from the bundle (MergedValue.Stored) is what it was
		// before, and so is its served run. A reply of an older value than
		// the holding's describes nothing it holds, as
		// applyHeldValueStateLocked ignores its state.
		if c.blobBacked && t.value.Stored && t.value.Replacements >= before {
			served := servedRun{replacements: t.value.Replacements}
			if t.served != nil && t.served.known {
				served.run, served.ran, served.known = t.served.run, t.served.ran, true
			}
			h.served = &served
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
			c.markHoldingLocked(change, key)
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
	return c.UpdatePreparedRemoteHolding(ctx, key, update, PrepareRemoteHoldingUpdate(key, update))
}

// UpdatePreparedRemoteHolding is UpdateRemoteHolding, with key and update
// prepared before (PrepareRemoteHoldingUpdate).
func (c *Cache) UpdatePreparedRemoteHolding(ctx context.Context, key HolderKey, update RemoteHoldingUpdate, prepared PreparedRemoteHoldingUpdate) (RemoteChange, bool, error) {
	if prepared.err != nil {
		return RemoteChange{}, false, prepared.err
	}
	c.lockGraphFor(ctx, workRemote)
	defer c.egraphMu.Unlock()
	change, found := c.updateRemoteHoldingLocked(ctx, key, update, prepared.parts)
	return change, found, nil
}

// PreparedRemoteHoldingUpdate is what updating a holding reads of the
// update without the cache (PrepareRemoteHoldingUpdate): its parts' keys, or
// why it can't apply. A holder that prepares it before it takes the cache's
// lock spends none of that in its hold.
type PreparedRemoteHoldingUpdate struct {
	parts []string
	err   error
}

// PrepareRemoteHoldingUpdate prepares update of key's holding, for
// UpdatePreparedRemoteHolding of the same key and update. It reads nothing of
// the cache, and of update only its parts.
func PrepareRemoteHoldingUpdate(key HolderKey, update RemoteHoldingUpdate) PreparedRemoteHoldingUpdate {
	parts, err := prepareRemoteHoldingUpdate(key, update)
	return PreparedRemoteHoldingUpdate{parts: parts, err: err}
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
	change.Candidates = c.applyHeldValueStateLocked(ctx, &change, c.remoteCaches[key.Cache], key, entry, h, HeldValueState{
		Replacements:  update.Replacements,
		Deps:          update.Deps,
		ContentDigest: update.ContentDigest,
	}, parts, nil)
	// An evaluation that reported a part complete ran the value at its
	// count.
	if update.Evaluated && ranPartsReported(parts) && c.noteHoldingRanLocked(&change, key, entry, h, update.Replacements, runRanByEvaluation) {
		change.Saved = true
	}
	return change, true
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
// keys. It returns the dropped dependencies' holdings. Requires egraphMu for
// writing.
func (c *Cache) applyHeldValueStateLocked(ctx context.Context, change *RemoteChange, state *remoteCacheState, key HolderKey, entry *sharedResult, h *holding, obs HeldValueState, parts, offered []string) []HolderKey {
	if obs.ContentDigest != "" && string(obs.ContentDigest) != handleString(h.contentDigest) {
		// The class learns a content digest from every observation, whatever
		// its count: a union is never undone, and gating it by count would
		// only make the class depend on arrival order. The holding's own
		// content digest follows the count below.
		c.teachRemoteIdentityLocked(ctx, change, entry, entry.description, "", "", obs.ContentDigest, nil)
	}
	if obs.Replacements < h.replacements {
		return nil
	}
	var candidates []HolderKey
	changed := false
	if obs.Replacements > h.replacements {
		changed = true
		h.replacements = obs.Replacements
		h.ran = false
		h.served = nil
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
			if _, dep := state.holdingLocked(number); dep != nil {
				// Its count of dependents is no reader's to keep
				// (HoldingFacts): it is a candidate, not marked.
				dep.dependents--
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
	// Its content digest and offered parts are read fresh (HoldingFacts): a
	// change to them alone names no holding. An offer never changes a
	// complete part, which is further along.
	if obs.ContentDigest != "" && string(obs.ContentDigest) != handleString(h.contentDigest) {
		h.contentDigest = internString(string(obs.ContentDigest))
	}
	advanceHeldParts(h, offered, heldPartOffered)
	if advanceHeldParts(h, parts, heldPartComplete) {
		changed = true
	}
	if changed {
		c.markHoldingLocked(change, key)
	}
	c.addHoldingDepsLocked(change, state, key, h, obs.Deps)
	return candidates
}

// AddRemoteHold records that session holds the holding.
func (c *Cache) AddRemoteHold(ctx context.Context, key HolderKey, session string) (RemoteChange, error) {
	if session == "" {
		return RemoteChange{}, errEmptyRemoteHoldSession
	}
	c.lockGraphFor(ctx, workRemote)
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
	// The sessions holding it are read fresh (HoldingFacts): the hold
	// names no holding.
	h.sessions, _ = withSession(h.sessions, session)
	state := c.remoteCaches[key.Cache]
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
	c.lockGraphFor(ctx, workRemote)
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
		if _, h := state.holdingLocked(number); h != nil {
			// The sessions holding it are read fresh (HoldingFacts): the
			// session's end makes it a candidate and names no holding.
			h.sessions, _ = withoutSession(h.sessions, session)
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
	c.lockGraphFor(ctx, workRemote)
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
		c.markHoldingLocked(&change, key)
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
	c.lockGraphFor(ctx, workRemote)
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
	c.lockGraphFor(ctx, workRemote)
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
	for number, held := range state.entries {
		key, h := HolderKey{Cache: cache, Number: number}, held.h
		// Its retention is a fact a reader keeps (HoldingFacts); the
		// sessions holding it are read fresh.
		if h.retention.retained {
			c.markHoldingLocked(&change, key)
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
// holding dependencies. owned, when set, is asked of each holding before it
// is removed, a candidate's or one the collection reaches through
// dependencies: one it reports owned by what the caller keeps beside the
// cache's holdings stays, as a holding with a dependent does. It is asked
// under the cache's lock, and must take none of the cache's. Each collected holding releases its entry's ownership
// unit, so entries left with no holding and no other owner are collected too.
// It returns the collected holdings' keys, sorted, and what it changed.
//
// It then compacts the e-graph's classes, at most once per
// eqClassCheckInterval and only when something changed since the last check
// (compactEqClassesAfterCollectionLocked), which frees the dead ones. Only the
// Cloud calls it, and never with snapshot sharing enabled, whose pending queue
// keeps class IDs. Engines compact through prune.
func (c *Cache) CollectRemoteHoldings(ctx context.Context, candidates []HolderKey, owned func(HolderKey) bool) ([]HolderKey, RemoteChange, error) {
	c.lockGraphFor(ctx, workRemote)
	collected, change, releases, err := c.collectRemoteHoldingsLocked(ctx, candidates, owned)
	c.egraphMu.Unlock()
	slices.SortFunc(collected, compareHolderKeys)
	return collected, change, errors.Join(err, runOnReleaseFuncs(ctx, releases))
}

// collectRemoteHoldingsLocked is CollectRemoteHoldings' change. It returns
// the collected holdings unsorted, and what the collected entries release,
// to run once egraphMu is released. Requires egraphMu for writing.
func (c *Cache) collectRemoteHoldingsLocked(ctx context.Context, candidates []HolderKey, owned func(HolderKey) bool) ([]HolderKey, RemoteChange, []OnReleaseFunc, error) {
	var change RemoteChange
	entriesBefore := len(c.resultsByID)
	var (
		queue     = slices.Clone(candidates)
		collected []HolderKey
		entries   []*sharedResult
		rerr      error
		// emptied are the engine caches the collection took a holding of:
		// only their states can be left with none.
		emptied = map[CacheID]struct{}{}
	)
	for len(queue) > 0 {
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		entry, h := c.holdingLocked(key)
		if h == nil || h.owned() || owned != nil && owned(key) {
			continue
		}
		deps, more, err := c.removeHoldingLocked(ctx, &change, c.remoteCaches[key.Cache], key, entry, h, entries)
		queue = append(queue, deps...)
		entries = more
		collected = append(collected, key)
		emptied[key.Cache] = struct{}{}
		rerr = errors.Join(rerr, err)
	}
	for id := range emptied {
		if state := c.remoteCaches[id]; state != nil && state.holdings == 0 {
			delete(c.remoteCaches, id)
			c.noteSharedWriteLocked(&change, SharedEngineCacheGone)
		}
	}
	releases, err := c.collectUnownedResultsLocked(ctx, entries)
	removed := len(c.resultsByID) < entriesBefore
	if removed {
		c.noteSharedWriteLocked(&change, SharedEntryRemoved)
	}
	c.compactEqClassesAfterCollectionLocked(&change, removed)
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
func (c *Cache) compactEqClassesAfterCollectionLocked(change *RemoteChange, removed bool) {
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
	c.noteSharedWriteLocked(change, SharedClassesChecked)
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
func (c *Cache) removeHoldingLocked(ctx context.Context, change *RemoteChange, state *remoteCacheState, key HolderKey, entry *sharedResult, h *holding, entries []*sharedResult) ([]HolderKey, []*sharedResult, error) {
	// An entry left with no holding keeps none: a stored entry outlives
	// every holding of it.
	entry.deleteHolderLocked(key)
	state.graphVersion++
	// The holding is marked, not its entry, as at its arrival
	// (addHoldingLocked). An entry its last holding leaves either stays,
	// stored, whose record answers whether it stores one, or goes with it,
	// an entry with no value being owned by its holdings alone, and its
	// leaving marks it (markLeavingLocked).
	c.markHoldingLocked(change, key)
	delete(state.entries, key.Number)
	state.holdings--
	c.remoteHoldings.Add(-1)
	var deps []HolderKey
	for _, number := range h.deps {
		if _, dep := state.holdingLocked(number); dep != nil {
			depKey := HolderKey{Cache: key.Cache, Number: number}
			// Its count of dependents is read fresh (HoldingFacts).
			dep.dependents--
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
	change, found, err := c.CollectGoneHoldings(ctx, []HolderKey{key})
	return change, found[0], err
}

// CollectGoneHoldings is CollectRemoteHolding for each of keys, in their
// order, in one write: a reply that names several holdings gone removes them
// waiting for the cache's lock once. found says, for each key, whether the
// cache had its holding. The candidates are the holdings the removed ones
// depended on, sorted, each once, and none the call removed itself.
func (c *Cache) CollectGoneHoldings(ctx context.Context, keys []HolderKey) (RemoteChange, []bool, error) {
	var change RemoteChange
	found := make([]bool, len(keys))
	removed := map[HolderKey]bool{}
	var (
		candidates []HolderKey
		entries    []*sharedResult
		rerr       error
	)
	c.lockGraphFor(ctx, workRemote)
	entriesBefore := len(c.resultsByID)
	for i, key := range keys {
		var deps []HolderKey
		var err error
		deps, entries, found[i], err = c.removeGoneHoldingLocked(ctx, &change, key, entries)
		if found[i] {
			removed[key] = true
		}
		candidates = append(candidates, deps...)
		rerr = errors.Join(rerr, err)
	}
	releases, collectErr := c.collectUnownedResultsLocked(ctx, entries)
	if len(c.resultsByID) < entriesBefore {
		c.eqClassRemoved = true
		c.noteSharedWriteLocked(&change, SharedEntryRemoved)
	}
	c.takeChangesLocked(&change)
	c.egraphMu.Unlock()
	candidates = slices.DeleteFunc(candidates, func(key HolderKey) bool { return removed[key] })
	slices.SortFunc(candidates, compareHolderKeys)
	change.Candidates = slices.Compact(candidates)
	return change, found, errors.Join(rerr, collectErr, runOnReleaseFuncs(ctx, releases))
}

// removeGoneHoldingLocked removes the holding key outright, for a gone
// answer (CollectRemoteHolding), and reports whether the cache had it. It
// returns the holdings it depended on, and appends to entries those its
// removal leaves to collect. Requires egraphMu for writing.
func (c *Cache) removeGoneHoldingLocked(ctx context.Context, change *RemoteChange, key HolderKey, entries []*sharedResult) ([]HolderKey, []*sharedResult, bool, error) {
	entry, h := c.holdingLocked(key)
	if h == nil {
		return nil, entries, false, nil
	}
	state := c.remoteCaches[key.Cache]
	if h.dependents > 0 {
		for number, held := range state.entries {
			parentKey, parent := HolderKey{Cache: key.Cache, Number: number}, held.h
			var had bool
			if parent.deps, had = withoutNumber(parent.deps, key.Number); !had {
				continue
			}
			parent.unknownDeps = withNumber(parent.unknownDeps, key.Number)
			c.markHoldingLocked(change, parentKey)
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
	deps, entries, err := c.removeHoldingLocked(ctx, change, state, key, entry, h, entries)
	if state.holdings == 0 {
		delete(c.remoteCaches, key.Cache)
		c.noteSharedWriteLocked(change, SharedEngineCacheGone)
	}
	return deps, entries, true, err
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
	c.lockGraphFor(ctx, workRemote)
	defer c.egraphMu.Unlock()
	change, found := c.applyHeldValueStateOfLocked(ctx, key, obs, parts, offered)
	return change, found, nil
}

// applyHeldValueStateOfLocked is ApplyHeldValueState's change, with the
// state's parts already keyed. Requires egraphMu for writing.
func (c *Cache) applyHeldValueStateOfLocked(ctx context.Context, key HolderKey, obs HeldValueState, parts, offered []string) (change RemoteChange, _ bool) {
	defer c.takeChangesLocked(&change)
	entry, h := c.holdingLocked(key)
	if h == nil {
		return change, false
	}
	change.Candidates = c.applyHeldValueStateLocked(ctx, &change, c.remoteCaches[key.Cache], key, entry, h, obs, parts, offered)
	return change, true
}

// addHoldingDepsLocked adds dependency numbers to a holding of the same cache:
// a number the cache holds is owned through that holding's dependents, and
// any other number waits in the reverse index until its holding is created.
// Requires egraphMu for writing.
func (c *Cache) addHoldingDepsLocked(change *RemoteChange, state *remoteCacheState, key HolderKey, h *holding, deps []uint64) {
	var known, unknown []uint64
	seen := make(map[uint64]struct{}, len(deps))
	for _, number := range deps {
		if _, ok := seen[number]; ok || number == key.Number || hasNumber(h.deps, number) || hasNumber(h.unknownDeps, number) {
			continue
		}
		seen[number] = struct{}{}
		if _, dep := state.holdingLocked(number); dep != nil {
			known = append(known, number)
			state.graphVersion++
			// The dependency's count of dependents is read fresh
			// (HoldingFacts): only the holding whose dependencies grew is
			// named.
			dep.dependents++
			c.markHoldingLocked(change, key)
			continue
		}
		unknown = append(unknown, number)
		c.markHoldingLocked(change, key)
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
func (c *Cache) teachRemoteIdentityLocked(ctx context.Context, change *RemoteChange, entry *sharedResult, field string, recipe, request, content digest.Digest, term *RemoteTerm) {
	// The entry's own frame takes its content digest, as the engine's did
	// when it was taught it, whether or not the e-graph learns anything: a
	// record sent from the Cloud then carries it, and a fresh engine joins
	// its class as the engine that computed it does. A digest taught again
	// after another is the frame's content again, as on the engine.
	if content != "" && entry != nil && entry.id != 0 {
		if n := len(entry.taught); n == 0 || entry.taught[n-1] != content {
			entry.taught = append(entry.taught, content)
			c.noteSharedWriteLocked(change, SharedTaughtContent)
		}
		given := entry.taughtGiven
		c.queueTaughtContentLocked(entry)
		if entry.taughtGiven != given {
			c.noteSharedWriteLocked(change, SharedTaughtContent)
		}
	}
	if c.remoteIdentityTeachesNothingLocked(entry, recipe, request, content, term) {
		return
	}
	c.noteSharedWriteLocked(change, SharedIdentity)
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
	defer c.rlockEngineLocked(key.Cache).runlock()
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
	if state == nil {
		return nil, true
	}
	r.lockEngine(key.Cache)
	if _, h := state.holdingLocked(key.Number); h == nil {
		return nil, true
	}
	version := state.graphVersion
	seen := map[uint64]bool{key.Number: true}
	queue := []uint64{key.Number}
	var keys []HolderKey
	for len(queue) > 0 {
		number := queue[0]
		queue = queue[1:]
		if _, h := state.holdingLocked(number); h != nil {
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

// HolderGraph returns the holdings in the closures of an engine cache's
// holdings numbered roots (HolderClosure), each with its dependencies that
// are holdings of the cache: the closures' graph, read in one walk however
// much they overlap. A holding in the closures of a thousand roots is read
// once, where a HolderClosure of each would read it a thousand times. A
// root the cache holds nothing for adds nothing. Like HolderClosure, it
// reads in pieces, and its answer is still one state's.
func (c *Cache) HolderGraph(cache CacheID, roots []uint64) map[uint64][]uint64 {
	for range holderClosureTries {
		r := c.readInPieces()
		r.lockEngine(cache)
		graph, ok := c.holderGraphLocked(cache, roots, r)
		r.done()
		if ok {
			return graph
		}
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	defer c.rlockEngineLocked(cache).runlock()
	graph, _ := c.holderGraphLocked(cache, roots, nil)
	return graph
}

// holderGraphLocked is HolderGraph's walk, in the pieces of r, or in one
// hold when r is nil. It reports false when the cache's holdings changed
// between two pieces: its graph would mix two states. Requires egraphMu,
// which r holds when it isn't nil.
func (c *Cache) holderGraphLocked(cache CacheID, roots []uint64, r *readPieces) (map[uint64][]uint64, bool) {
	graph := map[uint64][]uint64{}
	state := c.remoteCaches[cache]
	if state == nil {
		return graph, true
	}
	version := state.graphVersion
	seen := make(map[uint64]bool, len(roots))
	var stack []uint64
	for _, root := range roots {
		if !seen[root] {
			seen[root] = true
			stack = append(stack, root)
		}
	}
	for len(stack) > 0 {
		number := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, h := state.holdingLocked(number); h != nil {
			graph[number] = slices.Clone(h.deps)
			for _, dep := range h.deps {
				if !seen[dep] {
					seen[dep] = true
					stack = append(stack, dep)
				}
			}
		}
		if r != nil && len(stack) > 0 && r.step() && (c.remoteCaches[cache] != state || state.graphVersion != version) {
			return nil, false
		}
	}
	// A dependency with no holding is skipped, as HolderClosure skips it.
	for number, deps := range graph {
		graph[number] = slices.DeleteFunc(deps, func(dep uint64) bool {
			_, held := graph[dep]
			return !held
		})
	}
	return graph, true
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

// RemoteHoldingInfo describes one holding: its key, what a reader may keep of
// it from one change to the next (HoldingFacts), and what a reader reads
// fresh: its content digest, the sessions holding it, the count of its
// cache's holdings that depend on it, and its offered parts, the ones its
// cache has an offer of and not the bytes.
type RemoteHoldingInfo struct {
	Key HolderKey
	HoldingFacts
	ContentDigest digest.Digest
	Sessions      []string
	Dependents    int
	OfferedParts  []PersistedPartAddress
}

// RemoteEntryInfo returns what the cache holds for the entry of one holding,
// read under one hold.
func (c *Cache) RemoteEntryInfo(key HolderKey) (RemoteEntryInfo, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	state := c.rlockEngineLocked(key.Cache)
	entry, h := c.holdingLocked(key)
	state.runlock()
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
	defer c.rlockEngineLocked(key.Cache).runlock()
	return c.remoteHoldingInfoLocked(key)
}

// RemoteHoldingInfos is RemoteHoldingInfo for each of keys, in their order,
// read under one hold, as AvailableParts is AvailablePart: ok is false for a
// holding the cache doesn't have.
func (c *Cache) RemoteHoldingInfos(keys []HolderKey) (held []RemoteHeldEntry, ok []bool) {
	held, ok = make([]RemoteHeldEntry, len(keys)), make([]bool, len(keys))
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for i, key := range keys {
		state := c.rlockEngineLocked(key.Cache)
		held[i], ok[i] = c.remoteHoldingInfoLocked(key)
		state.runlock()
		if c.testBatchAnswered != nil {
			c.testBatchAnswered(i)
		}
		if i+1 < len(keys) {
			c.giveWayLocked()
		}
	}
	return held, ok
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
	defer c.rlockEngineLocked(key.Cache).runlock()
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

// EntryRecipe returns a recipe that names the entry numbered number in the
// cache's recipe index: one an engine's holding of the entry attaches by
// (RemoteHolding.Recipe), as when the Cloud learns that an engine holds an
// entry other than by the engine's own call span. It reports false when the
// cache has no such entry, or no recipe names it.
func (c *Cache) EntryRecipe(number uint64) (digest.Digest, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.entryRecipeLocked(number)
}

// entryRecipeLocked is EntryRecipe's answer. Requires egraphMu.
func (c *Cache) entryRecipeLocked(number uint64) (digest.Digest, bool) {
	entry := c.resultsByID[sharedResultID(number)]
	if entry == nil || len(entry.recipeKeys) == 0 {
		return "", false
	}
	return entry.recipeKeys[0], true
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
	defer c.rlockEngineLocked(cache).runlock()
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
	for i, number := range numbers {
		if i > 0 {
			c.giveWayLocked()
		}
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
		if limit > 0 {
			entry.eachHolderLocked(func(ref holderRef) bool {
				held.Keys = append(held.Keys, ref.key)
				return len(held.Keys) < limit
			})
		}
		out = append(out, held)
		if c.testBatchAnswered != nil {
			c.testBatchAnswered(len(out) - 1)
		}
	}
	return out
}

// EntryFacts is an entry's own facts, as RemoteHoldingInfo reports them with
// any holding on it: its number, whether it stores a record and that
// record's expiry, and its recipes, sorted.
type EntryFacts struct {
	Number              uint64
	Stored              bool
	StoredExpiresAtUnix int64
	Recipes             []digest.Digest
}

// EntriesFacts returns, for each of numbers the cache has an entry for, in
// their order, the entry's own facts (EntryFacts), read under one hold. A
// number the cache has no entry for is skipped: a caller matches the results
// by Number. It describes no holding, so a reader that keeps holdings'
// descriptions reads only what an entry named by a change (RemoteChange's
// Entries) can have changed of them.
func (c *Cache) EntriesFacts(numbers []uint64) []EntryFacts {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	out := make([]EntryFacts, 0, len(numbers))
	for i, number := range numbers {
		if i > 0 {
			c.giveWayLocked()
		}
		entry := c.resultsByID[sharedResultID(number)]
		if entry == nil {
			continue
		}
		var facts EntryFacts
		facts.Number, facts.Stored, facts.StoredExpiresAtUnix, facts.Recipes = entryFactsLocked(entry)
		out = append(out, facts)
		if c.testBatchAnswered != nil {
			c.testBatchAnswered(len(out) - 1)
		}
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
	// Each holding is read under its own engine cache's lock, one at a
	// time: two engines' holdings may be read a write apart.
	for _, ref := range entry.holderRefsLocked() {
		state := c.rlockEngineLocked(ref.key.Cache)
		info.Holdings = append(info.Holdings, holdingInfo(ref.key, ref.h))
		state.runlock()
	}
	return info
}

func holdingInfo(key HolderKey, h *holding) RemoteHoldingInfo {
	info := RemoteHoldingInfo{
		Key: key,
		HoldingFacts: HoldingFacts{
			TypeName:          handleString(h.typeName),
			ExpiresAtUnix:     h.expiresAtUnix,
			Retained:          h.retention.retained,
			Replacements:      h.replacements,
			Executed:          h.executed,
			FollowsFromInputs: h.follows,
			CallRow:           h.rowed,
			Deps:              slices.Clone(h.deps),
			UnknownDeps:       slices.Clone(h.unknownDeps),
		},
		ContentDigest: digest.Digest(handleString(h.contentDigest)),
		Dependents:    int(h.dependents),
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

// HoldingFacts are what a reader may keep of a holding from one change to
// the next, the rest of its description (RemoteHoldingInfo) being read
// fresh: its type, its copy's expiry, its retention and that retention's
// expiry, the replacement count the value state describes, whether its
// cache computed the entry since the cache last dropped the entry's value
// (RemoteHolding.Executed), its dependencies, those its cache has no holding
// of yet (UnknownDeps), and its copy's complete parts. Its entry's own facts
// are the entry's (EntryFacts).
type HoldingFacts struct {
	TypeName               string
	ExpiresAtUnix          int64
	Retained               bool
	RetentionExpiresAtUnix int64
	Replacements           uint64
	Executed               bool
	// FollowsFromInputs and CallRow are the holding's word on the
	// declaration, as a call row's description set it (Declares).
	FollowsFromInputs, CallRow bool
	Deps                       []uint64
	UnknownDeps                []uint64
	Parts                      []PersistedPartAddress
}

// HoldingFacts returns the facts of the holding key, read under one hold,
// without copying its dependencies: Deps and UnknownDeps are the holding's
// own slices, which a write replaces and never changes, so they stay as
// read, and the caller must not change them. It reports false when the
// cache has no such holding.
func (c *Cache) HoldingFacts(key HolderKey) (HoldingFacts, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	defer c.rlockEngineLocked(key.Cache).runlock()
	_, h := c.holdingLocked(key)
	if h == nil {
		return HoldingFacts{}, false
	}
	facts := HoldingFacts{
		TypeName:          handleString(h.typeName),
		ExpiresAtUnix:     h.expiresAtUnix,
		Retained:          h.retention.retained,
		Replacements:      h.replacements,
		Executed:          h.executed,
		FollowsFromInputs: h.follows,
		CallRow:           h.rowed,
		Deps:              h.deps,
		UnknownDeps:       h.unknownDeps,
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
