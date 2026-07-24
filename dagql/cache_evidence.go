package dagql

import (
	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// CacheOutcome is what the cache decided for one call. The values are the
// telemetryattrs.CacheOutcome* wire tokens.
type CacheOutcome string

const (
	CacheOutcomeHit      CacheOutcome = telemetryattrs.CacheOutcomeHit
	CacheOutcomeExecuted CacheOutcome = telemetryattrs.CacheOutcomeExecuted
	CacheOutcomeJoined   CacheOutcome = telemetryattrs.CacheOutcomeJoined
	CacheOutcomeUncached CacheOutcome = telemetryattrs.CacheOutcomeUncached
)

// CacheHitRoute is how a cache hit was found. The values are the
// telemetryattrs.CacheHitRoute* wire tokens.
type CacheHitRoute string

const (
	CacheHitRouteRecipe     CacheHitRoute = telemetryattrs.CacheHitRouteRecipe
	CacheHitRouteDigest     CacheHitRoute = telemetryattrs.CacheHitRouteDigest
	CacheHitRouteStructural CacheHitRoute = telemetryattrs.CacheHitRouteStructural
)

// CacheDecision is the per-invocation cache-evidence carrier: a plain record of
// the decision facts for one GetOrInitCall invocation, filled by dagql along
// the existing control flow and consumed by core.AroundFunc's completion
// callback, which maps it onto the caller's already-existing span as the
// dagger.io/cache.* attribute vocabulary (engine/telemetryattrs).
//
// Ownership: core allocates it onto CallRequest.CacheEvidence exactly when the
// call's span records and the call is not ProfileSkip-classified; dagql only
// ever fills a non-nil carrier (nil means "record nothing" and costs nothing).
// Every write happens on the invoking goroutine with values already in hand —
// the carrier adds no locking and never alters any cache decision. Population
// is best-effort: a fact whose derivation fails is dropped, never failing the
// call.
type CacheDecision struct {
	// Outcome is the call's cache outcome. Empty means the invocation never
	// reached a decision (e.g. it errored during validation or identity
	// derivation); nothing is stamped then.
	Outcome CacheOutcome

	// HitRoute is how the hit was found. Set only for Outcome == CacheOutcomeHit.
	HitRoute CacheHitRoute

	// MissIncompatibleCandidates records that the lookup's post-expiry
	// candidate set was non-empty but no candidate satisfied this session's
	// resource requirements. Meaningful only for misses (Outcome executed).
	MissIncompatibleCandidates bool

	// MissSawExpired records that TTL expiry eliminated at least one
	// otherwise-matching result during candidate accumulation. Meaningful only
	// for misses (Outcome executed).
	MissSawExpired bool

	// MissUnknownInputIndex is the index into StructuralInputs of the first
	// input digest that had no equivalence class at lookup time (making the
	// structural lookup impossible), or -1 when every input was known.
	// Meaningful only for misses (Outcome executed).
	MissUnknownInputIndex int

	// SelfDigest is the engine-derived structural self digest of the call.
	// Empty for CacheOutcomeUncached, whose path skips identity derivation.
	SelfDigest digest.Digest

	// StructuralInputs is the exact ordered structural-input digest list used
	// for the equivalence lookup (receiver, reference-valued arguments in
	// order, digest witnesses, module). Nil for CacheOutcomeUncached.
	StructuralInputs []digest.Digest

	// PairingDigest is the self digest derived with implicit inputs excluded —
	// the cross-run pairing anchor. Empty for CacheOutcomeUncached.
	PairingDigest digest.Digest
}

// NewCacheDecision returns an empty carrier with its index sentinel set.
func NewCacheDecision() *CacheDecision {
	return &CacheDecision{MissUnknownInputIndex: -1}
}
