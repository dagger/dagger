package dagql

import (
	"context"
	"errors"
	"slices"

	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// cacheUsageSnapshot owns the rows while provider callbacks run outside E.
// A Go pointer alone would not prevent OnRelease from closing their resources.
// Mutable provider identities remain best-effort samples: payloadRevision
// detects replacement of a row payload, not mutations inside its provider.
// The same sampled identities feed both disk-prune accounting passes.
type cacheUsageSnapshot struct {
	cache        *Cache
	op           cacheOperation
	inputs       map[sharedResultID]cacheUsageMeasurementInput
	holds        map[*sharedResult]struct{}
	measurements map[string]cacheUsageIdentityMeasurement
	chains       snapshotChains
	released     bool
	callbacks    []OnReleaseFunc
	releaseErr   error
}

const cacheUsageMaxSamplingRounds = 3

func (c *Cache) collectUsageMeasurementInputs(ctx context.Context, measure bool) (*cacheUsageSnapshot, error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, err
	}
	snapshot := &cacheUsageSnapshot{
		cache: c, op: op,
		inputs:       make(map[sharedResultID]cacheUsageMeasurementInput),
		holds:        make(map[*sharedResult]struct{}),
		measurements: make(map[string]cacheUsageIdentityMeasurement),
		chains:       c.newSnapshotChains(),
	}
	for round := range cacheUsageMaxSamplingRounds {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, snapshot.close(ctx))
		}
		c.egraphMu.Lock()
		pending := snapshot.capturePendingLocked(ctx)
		c.egraphMu.Unlock()
		if len(pending) == 0 {
			break
		}
		if round > 0 {
			slog.Debug("cache usage resampling changed population", "round", round+1, "rows", len(pending))
		}
		for i := range pending {
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(err, snapshot.close(ctx))
			}
			input := &pending[i]
			if input.self != nil {
				input.identities = cacheUsageIdentitiesFromSelf(input.self)
				input.sizeMayChange = cacheUsageSizeMayChangeFromSelf(input.self)
			} else {
				input.identities = cacheUsageIdentitiesFromSnapshotLinks(input.snapshotLinks)
			}
			snapshot.chains.load(ctx, input.identities)
			input.ownIdentities = input.identities
			input.identities, input.chainsIncomplete = snapshot.chains.expand(input.identities)
			snapshot.inputs[input.resultID] = *input
		}
		if measure {
			measured := buildCacheUsageMeasurements(ctx, c.snapshotManager, pending)
			for _, byIdentity := range measured {
				for identity, measurement := range byIdentity {
					snapshot.measurements[identity] = measurement
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, snapshot.close(ctx))
	}
	c.keepSnapshotChains(snapshot.chains)
	// Other passes now read these maps without a lock, so rows that only
	// finalization sees must expand without caching anything.
	snapshot.chains.published = true
	return snapshot, nil
}

func cacheUsageInputLocked(res *sharedResult) cacheUsageMeasurementInput {
	state := res.loadPayloadState()
	input := cacheUsageMeasurementInput{
		resultID: res.id, row: res, payloadRevision: state.payloadRevision,
		existingSizeByID: make(map[string]int64, len(res.cacheUsageSizeByIdentity)),
	}
	if state.hasValue && state.self != nil {
		input.self = state.self
	} else {
		input.snapshotLinks = cloneSnapshotRefLinks(state.snapshotOwnerLinks)
	}
	for identity, size := range res.cacheUsageSizeByIdentity {
		input.existingSizeByID[identity] = size
	}
	return input
}

// Identity-free values need no provider callback. Unmaterialized values instead
// contribute their actual snapshot links, which may share measured identities.
func (input *cacheUsageMeasurementInput) identitiesWithoutCallbacks() bool {
	if input.self == nil {
		input.identities = cacheUsageIdentitiesFromSnapshotLinks(input.snapshotLinks)
		return true
	}
	if _, provider := input.self.(hasCacheUsageIdentity); provider {
		return false
	}
	input.identities = nil
	return true
}

func (snapshot *cacheUsageSnapshot) capturePendingLocked(ctx context.Context) []cacheUsageMeasurementInput {
	var pending []cacheUsageMeasurementInput
	for id, res := range snapshot.cache.resultsByID {
		if input, ok := snapshot.inputs[id]; ok && input.validLocked(snapshot.cache) {
			continue
		}
		if _, held := snapshot.holds[res]; !held {
			snapshot.cache.incrementIncomingOwnershipLocked(ctx, res)
			snapshot.holds[res] = struct{}{}
		}
		input := cacheUsageInputLocked(res)
		if input.identitiesWithoutCallbacks() && len(input.identities) == 0 {
			snapshot.inputs[id] = input
			continue
		}
		pending = append(pending, input)
	}
	return pending
}

// finalizeLocked must run after real hold collection and before graph/count
// copying. Unknown provider membership prevents ALL physical reclaim credit;
// never feed a zero-credit fallback into a planner that can evict every root.
func (snapshot *cacheUsageSnapshot) finalizeLocked() (map[sharedResultID][]string, error) {
	identities := make(map[sharedResultID][]string, len(snapshot.cache.resultsByID))
	unknown := 0
	for id, res := range snapshot.cache.resultsByID {
		input, sampled := snapshot.inputs[id]
		if !sampled || !input.validLocked(snapshot.cache) {
			input = cacheUsageInputLocked(res)
			if !input.identitiesWithoutCallbacks() {
				unknown++
				continue
			}
			// Parents are only looked up outside E, so a row first seen here
			// expands only as far as this pass already resolved its chain.
			input.ownIdentities = input.identities
			input.identities, input.chainsIncomplete = snapshot.chains.expand(input.identities)
			snapshot.inputs[id] = input
		}
		// A row missing some of its ancestors would let the simulation credit
		// layers it still retains once their other holders are collected.
		if input.chainsIncomplete {
			unknown++
			continue
		}
		identities[id] = input.identities
	}
	if unknown > 0 {
		slog.Debug("cache usage sampling incomplete; preserving previous sizes and deferring prune", "unsampledRows", unknown, "maxRounds", cacheUsageMaxSamplingRounds)
		return nil, errCacheUsageChanged
	}
	return identities, nil
}

func (input cacheUsageMeasurementInput) validLocked(c *Cache) bool {
	return c.resultsByID[input.resultID] == input.row && input.row.loadPayloadState().payloadRevision == input.payloadRevision
}

// releaseLocked removes measurement ownership before pruning copies incoming
// counts. Callbacks remain deferred until after E is released.
func (snapshot *cacheUsageSnapshot) releaseLocked(ctx context.Context) {
	if snapshot.released {
		return
	}
	snapshot.released = true
	var queue []*sharedResult
	for row := range snapshot.holds {
		var err error
		queue, err = snapshot.cache.decrementIncomingOwnershipLocked(ctx, row, queue)
		snapshot.releaseErr = errors.Join(snapshot.releaseErr, err)
	}
	var err error
	snapshot.callbacks, err = snapshot.cache.collectUnownedResultsLocked(ctx, queue)
	snapshot.releaseErr = errors.Join(snapshot.releaseErr, err)
}

func (snapshot *cacheUsageSnapshot) close(ctx context.Context) error {
	defer snapshot.op.finish(false)
	ctx = context.WithoutCancel(ctx)
	if !snapshot.released {
		snapshot.cache.egraphMu.Lock()
		snapshot.releaseLocked(ctx)
		snapshot.cache.egraphMu.Unlock()
	}
	err := errors.Join(snapshot.releaseErr, runOnReleaseFuncs(ctx, snapshot.callbacks))
	if err != nil {
		// These may be callbacks deferred by a concurrent session release.
		snapshot.cache.recordReleaseCleanupError("", true, err)
	}
	return err
}

// snapshotChains resolves snapshot parent chains for usage passes.
//
// An owner lease retains its snapshot's whole parent chain, so a result's
// usage identities are that chain, not just its top snapshot. Otherwise the
// lower layers of an image, or the layers under a result whose own entry is
// gone, stay on disk without being counted anywhere. Shared layers are
// deduplicated by identity like any other shared snapshot, and the prune
// simulation only credits a layer once every result listing it is collected.
//
// A snapshot's chain never changes, so each pass reuses the parents and
// sorted chains the previous pass resolved, and keeps only the ones it
// visited: the carried maps stay bounded by the live snapshots instead of
// every snapshot ever seen. Rows on the same snapshot share its chain slice,
// which must not be modified. Lookups do I/O, so load runs outside E; expand
// only reads what the pass has resolved.
type snapshotChains struct {
	manager bkcache.SnapshotManager
	// knownParents and knownChains hold the previous pass's results and are
	// never written.
	knownParents map[string]string
	knownChains  map[string][]string
	// parents maps each snapshot this pass visited to its parent, "" for a
	// base snapshot.
	parents map[string]string
	// chains maps a snapshot to its sorted complete chain: itself and every
	// ancestor.
	chains map[string][]string
	// published reports that parents and chains were handed to later passes
	// and must no longer be written.
	published bool
}

type snapshotChainMemo struct {
	parents map[string]string
	chains  map[string][]string
}

func (c *Cache) newSnapshotChains() snapshotChains {
	c.usageSnapshotChainsMu.Lock()
	known := c.usageSnapshotChains
	c.usageSnapshotChainsMu.Unlock()
	return snapshotChains{
		manager:      c.snapshotManager,
		knownParents: known.parents,
		knownChains:  known.chains,
		parents:      make(map[string]string),
		chains:       make(map[string][]string),
	}
}

func (c *Cache) keepSnapshotChains(chains snapshotChains) {
	c.usageSnapshotChainsMu.Lock()
	c.usageSnapshotChains = snapshotChainMemo{parents: chains.parents, chains: chains.chains}
	c.usageSnapshotChainsMu.Unlock()
}

// load resolves the parent chain of each snapshot not already visited.
func (chains snapshotChains) load(ctx context.Context, snapshotIDs []string) {
	if chains.manager == nil {
		return
	}
	for _, id := range snapshotIDs {
		for id != "" {
			if _, visited := chains.parents[id]; visited {
				break
			}
			parent, known := chains.knownParents[id]
			if !known {
				var err error
				parent, err = chains.manager.SnapshotParent(ctx, id)
				if bkcache.IsNotFound(err) {
					// The snapshot is gone, so it retains nothing below it.
					parent, err = "", nil
				}
				if err != nil {
					// Leave the link unresolved: the chain stays incomplete
					// and the next pass retries.
					slog.Warn("failed to resolve snapshot parent for cache usage", "snapshotID", id, "err", err)
					break
				}
			}
			chains.parents[id] = parent
			id = parent
		}
	}
}

// chain returns the snapshot's sorted complete chain, or false if some link
// in it is unresolved.
func (chains snapshotChains) chain(id string) ([]string, bool) {
	if chain, ok := chains.chains[id]; ok {
		return chain, true
	}
	if chains.published {
		return chains.uncachedChain(id)
	}
	if chain, ok := chains.knownChains[id]; ok {
		// Still mark every link visited, so the next pass keeps the parents
		// a new snapshot on top of this chain would need.
		complete := true
		for _, link := range chain {
			parent, ok := chains.parents[link]
			if !ok {
				parent, ok = chains.knownParents[link]
			}
			if !ok {
				complete = false
				break
			}
			chains.parents[link] = parent
		}
		if complete {
			chains.chains[id] = chain
			return chain, true
		}
	}
	chain, ok := chains.uncachedChain(id)
	if ok {
		chains.chains[id] = chain
	}
	return chain, ok
}

// uncachedChain assembles the snapshot's sorted chain from this pass's
// parents without writing anything.
func (chains snapshotChains) uncachedChain(id string) ([]string, bool) {
	var chain []string
	for link := id; link != ""; {
		parent, resolved := chains.parents[link]
		if !resolved {
			return nil, false
		}
		chain = append(chain, link)
		link = parent
	}
	slices.Sort(chain)
	return chain, true
}

// expand returns the snapshots plus every ancestor, sorted and deduplicated.
// incomplete reports that some link in the chains is unresolved, so the
// result may not list every snapshot retained.
func (chains snapshotChains) expand(snapshotIDs []string) (expanded []string, incomplete bool) {
	if len(snapshotIDs) == 0 || chains.manager == nil {
		return snapshotIDs, false
	}
	if len(snapshotIDs) == 1 {
		chain, ok := chains.chain(snapshotIDs[0])
		if !ok {
			return snapshotIDs, true
		}
		return chain, false
	}
	for _, id := range snapshotIDs {
		chain, ok := chains.chain(id)
		if !ok {
			return snapshotIDs, true
		}
		expanded = append(expanded, chain...)
	}
	slices.Sort(expanded)
	return slices.Compact(expanded), false
}
