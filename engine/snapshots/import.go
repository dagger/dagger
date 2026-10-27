package snapshots

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/engine/wcprof"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// ImportChain imports one immutable chain from an in-process provider. Snapshot
// reuse precedes byte access. The returned ref owns its resources until Release,
// giving an ordinary caller time to attach its own snapshot lease.
//
// The blobs of the layers after the one importing are downloaded ahead, up to
// chainAheadWindow of them, from the first layer the engine doesn't already
// have imported. Layers apply strictly in order, unless the snapshotter can
// rebase (cm.applyLayersApart): the layers from that first one then apply at
// once, up to chainAheadWindow of them, and are committed onto their parents
// in order.
func (cm *snapshotManager) ImportChain(ctx context.Context, chain *ExportChain) (_ ImmutableRef, rerr error) {
	if chain == nil {
		return nil, fmt.Errorf("import snapshot chain: nil chain")
	}
	if len(chain.Layers) == 0 {
		return cm.Scratch(ctx)
	}
	pin, ctx, err := cm.newResourcePin(ctx)
	if err != nil {
		return nil, err
	}
	var current ImmutableRef
	ahead := &chainAhead{cm: cm, chain: chain, held: map[int]*contentFetch{}}
	defer func() {
		if rerr != nil {
			ahead.drop(ctx)
			if current != nil {
				_ = current.Release(context.WithoutCancel(ctx))
			}
			_ = pin.release(ctx)
		}
	}()
	first := cm.chainLocalPrefix(chain)
	// importInOrder imports layer i onto current, as the next in order.
	importInOrder := func(i int) error {
		if i >= first {
			ahead.fill(ctx, i)
		}
		held := ahead.take(i)
		// A layer that needed its bytes after all, as when its snapshot is
		// gone, starts the downloads after it too.
		layerImport := &chainLayer{owner: chain, ahead: held, fetched: func() { ahead.fill(ctx, i) }}
		next, err := cm.importLayer(ctx, chain.Layers[i].Descriptor, current, chain.Provider, ImportImageOpts{}, true, chain.Timing, layerImport)
		if err != nil {
			cm.dropFetch(ctx, held)
			return err
		}
		cm.leaveFetch(ctx, held)
		if current != nil {
			_ = current.Release(context.WithoutCancel(ctx))
		}
		current = next
		return nil
	}
	apartFrom := len(chain.Layers)
	if cm.applyLayersApart {
		apartFrom = first
	}
	for i := range apartFrom {
		if err := importInOrder(i); err != nil {
			return nil, err
		}
	}
	if apartFrom < len(chain.Layers) {
		if err := cm.importChainApart(ctx, chain, apartFrom, ahead, &current, importInOrder); err != nil {
			return nil, err
		}
	}
	current.(*immutableRef).pin = pin
	return current, nil
}

// appliedLayer is a chain layer applied apart: its snapshot, prepared with no
// parent, or nil when another import already had the layer, and the import
// lock of the layer's chain, held until the layer is committed in order.
type appliedLayer struct {
	mut    MutableRef
	unlock func()
	timing ImportTiming
	err    error
}

func (a appliedLayer) release(ctx context.Context) {
	if a.mut != nil {
		_ = a.mut.Release(context.WithoutCancel(ctx))
	}
	if a.unlock != nil {
		a.unlock()
	}
}

// importChainApart imports chain's layers from first onto *current: each
// applies into a snapshot of its own as soon as its blob is in, up to
// chainAheadWindow past the one committing, and is committed onto its parent
// in order. A layer another import already has is imported in order instead,
// which reuses it.
func (cm *snapshotManager) importChainApart(ctx context.Context, chain *ExportChain, first int, ahead *chainAhead, current *ImmutableRef, importInOrder func(int) error) (rerr error) {
	chainIDs, err := chainIDsFrom(chain)
	if err != nil {
		return err
	}
	applyCtx, cancel := context.WithCancelCause(ctx)
	results := make([]chan appliedLayer, len(chain.Layers))
	next := first
	defer func() {
		cancel(nil)
		for _, ch := range results {
			if ch != nil {
				(<-ch).release(ctx)
			}
		}
	}()
	for i := first; i < len(chain.Layers); i++ {
		for ; next < len(chain.Layers) && next <= i+chainAheadWindow; next++ {
			ahead.fill(ctx, next)
			ch := make(chan appliedLayer, 1)
			results[next] = ch
			go func(j int, held *contentFetch) {
				ch <- cm.applyChainLayer(applyCtx, chain, j, chainIDs[j], held)
			}(next, ahead.take(next))
		}
		applied := <-results[i]
		results[i] = nil
		if chain.Timing != nil {
			chain.Timing.add(applied.timing)
		}
		if applied.err != nil {
			applied.release(ctx)
			return applied.err
		}
		if applied.mut == nil {
			err = importInOrder(i)
		} else {
			var ref ImmutableRef
			ref, err = cm.commitAppliedLayer(ctx, applied.mut, chain.Layers[i].Descriptor, *current, chain)
			applied.mut = nil
			if err == nil {
				if *current != nil {
					_ = (*current).Release(context.WithoutCancel(ctx))
				}
				*current = ref
			}
		}
		applied.release(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

// chainIDsFrom returns the chain ID of each of chain's layers: the identity
// of the layers up to and including it.
func chainIDsFrom(chain *ExportChain) ([]digest.Digest, error) {
	diffIDs := make([]digest.Digest, len(chain.Layers))
	for i, layer := range chain.Layers {
		diffID, err := diffIDFromDescriptor(layer.Descriptor)
		if err != nil {
			return nil, err
		}
		diffIDs[i] = diffID
	}
	chainIDs := make([]digest.Digest, len(diffIDs))
	for i := range diffIDs {
		chainIDs[i] = identity.ChainID(diffIDs[:i+1])
	}
	return chainIDs, nil
}

// applyChainLayer applies chain's layer i into a snapshot of its own,
// prepared with no parent, under the import lock of the layer's chain. Another
// import of the same layers holds that lock until it has committed the
// layer, which this import then reuses: it applies nothing. held is the
// layer's download started ahead, if any, which it lets go of.
func (cm *snapshotManager) applyChainLayer(ctx context.Context, chain *ExportChain, i int, chainID digest.Digest, held *contentFetch) (applied appliedLayer) {
	desc := chain.Layers[i].Descriptor
	clock := importClock{timing: &applied.timing, prof: wcprof.Enabled(ctx)}
	defer func() {
		if applied.err != nil {
			cm.dropFetch(ctx, held)
		} else {
			cm.leaveFetch(ctx, held)
		}
	}()
	applied.unlock, applied.err = cm.acquireImportLock(ctx, "chain/"+chainID.String(), clock)
	if applied.err != nil {
		return applied
	}
	if cm.chainLocalPrefix(chain) > i {
		return applied
	}
	if applied.err = cm.fetchLayerContent(ctx, desc, chain.Provider, true, &chainLayer{owner: chain, ahead: held}, clock); applied.err != nil {
		return applied
	}
	mut, err := cm.New(ctx, nil, nil, WithDescription(fmt.Sprintf("import snapshot layer %s", desc.Digest)))
	if err != nil {
		applied.err = err
		return applied
	}
	if applied.err = cm.applyLayer(ctx, mut, desc, true, clock, true); applied.err != nil {
		_ = mut.Release(context.WithoutCancel(ctx))
		return applied
	}
	applied.mut = mut
	return applied
}

// commitAppliedLayer commits mut, desc's layer of chain applied apart, onto
// parent, unless the engine has imported the layer on parent meanwhile: it
// then reuses that and releases mut. It takes ownership of mut.
func (cm *snapshotManager) commitAppliedLayer(ctx context.Context, mut MutableRef, desc ocispecs.Descriptor, parent ImmutableRef, chain *ExportChain) (_ ImmutableRef, rerr error) {
	timing := chain.Timing
	defer func() {
		if mut != nil {
			_ = mut.Release(context.WithoutCancel(ctx))
		}
	}()
	diffID, err := diffIDFromDescriptor(desc)
	if err != nil {
		return nil, err
	}
	parentSnapshotID := ""
	if parent != nil {
		parentSnapshotID = parent.SnapshotID()
	}
	clock := importClock{timing: timing, prof: wcprof.Enabled(ctx)}
	unlock, err := cm.acquireImportLock(ctx, importedLayerDiffLockKey(parentSnapshotID, diffID), clock)
	if err != nil {
		return nil, err
	}
	defer unlock()
	blobKey := ImportedLayerBlobKey{ParentSnapshotID: parentSnapshotID, BlobDigest: desc.Digest}
	diffKey := ImportedLayerDiffKey{ParentSnapshotID: parentSnapshotID, DiffID: diffID}
	if ref, err := cm.reuseImportedLayer(ctx, blobKey, diffKey, ImportImageOpts{}); err != nil || ref != nil {
		return ref, err
	}
	var ref ImmutableRef
	if parent == nil {
		ref, err = mut.Commit(ctx)
	} else {
		leaseID, _ := leases.FromContext(ctx)
		if err := cm.AttachLease(ctx, leaseID, parentSnapshotID); err != nil {
			return nil, err
		}
		ref, err = mut.(*mutableRef).commitOnto(ctx, parent)
	}
	if err != nil {
		return nil, err
	}
	mut = nil
	ref, err = cm.recordImportedLayer(ctx, ref, desc, diffID, ImportImageOpts{}, blobKey, diffKey)
	if err == nil && chain.Applied != nil {
		chain.Applied(ctx, desc, ref.SnapshotID())
	}
	return ref, err
}

// chainLocalPrefix is how many of chain's first layers the engine has
// imported already, on the same parents, by the import indexes alone. Those
// need no bytes. An index entry whose snapshot has gone since only means its
// layer downloads when it imports.
func (cm *snapshotManager) chainLocalPrefix(chain *ExportChain) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	parent := ""
	for i, layer := range chain.Layers {
		snapshotID := cm.importedLayerByBlob[ImportedLayerBlobKey{ParentSnapshotID: parent, BlobDigest: layer.Descriptor.Digest}]
		if snapshotID == "" {
			if diffID, err := diffIDFromDescriptor(layer.Descriptor); err == nil {
				snapshotID = cm.importedLayerByDiff[ImportedLayerDiffKey{ParentSnapshotID: parent, DiffID: diffID}]
			}
		}
		if snapshotID == "" {
			return i
		}
		parent = snapshotID
	}
	return len(chain.Layers)
}

// chainAhead is one chain import's downloads of the blobs of the layers after
// the one it imports, by layer. It is used by the importing goroutine only.
type chainAhead struct {
	cm    *snapshotManager
	chain *ExportChain
	held  map[int]*contentFetch
	// next is the first layer fill hasn't considered yet.
	next int
}

// fill starts the downloads of the layers after layer i, in layer order, up
// to chainAheadWindow past it. It stops at the first that finds no free slot;
// a later fill tries that layer again.
func (a *chainAhead) fill(ctx context.Context, i int) {
	if a.chain.Provider == nil {
		return
	}
	a.next = max(a.next, i+1)
	for ; a.next < len(a.chain.Layers) && a.next <= i+chainAheadWindow; a.next++ {
		f, started, full := a.cm.fetchAhead(ctx, a.chain.Layers[a.next].Descriptor, a.chain.Provider, a.chain)
		if full {
			return
		}
		if f != nil {
			a.held[a.next] = f
		}
		if started && a.chain.Timing != nil {
			a.chain.Timing.Ahead++
		}
	}
}

// take hands layer i's download, if any, to its import.
func (a *chainAhead) take(i int) *contentFetch {
	f := a.held[i]
	delete(a.held, i)
	return f
}

// drop lets go of every download still held, waiting for those it abandons
// to stop and release their leases.
func (a *chainAhead) drop(ctx context.Context) {
	for i, f := range a.held {
		delete(a.held, i)
		a.cm.dropFetch(ctx, f)
	}
}
