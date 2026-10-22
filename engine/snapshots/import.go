package snapshots

import (
	"context"
	"fmt"
)

// ImportChain imports one immutable chain from an in-process provider. Snapshot
// reuse precedes byte access. The returned ref owns its resources until Release,
// giving an ordinary caller time to attach its own snapshot lease.
//
// Layers apply strictly in order. The blobs of the layers after the one
// importing are downloaded ahead, up to chainAheadWindow of them, from the
// first layer the engine doesn't already have imported.
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
	for i, layer := range chain.Layers {
		if i >= first {
			ahead.fill(ctx, i)
		}
		held := ahead.take(i)
		// A layer that needed its bytes after all, as when its snapshot is
		// gone, starts the downloads after it too.
		layerImport := &chainLayer{owner: chain, ahead: held, fetched: func() { ahead.fill(ctx, i) }}
		next, err := cm.importLayer(ctx, layer.Descriptor, current, chain.Provider, ImportImageOpts{}, true, chain.Timing, layerImport)
		if err != nil {
			cm.dropFetch(ctx, held)
			return nil, err
		}
		cm.leaveFetch(ctx, held)
		if current != nil {
			_ = current.Release(context.WithoutCancel(ctx))
		}
		current = next
	}
	current.(*immutableRef).pin = pin
	return current, nil
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
	if a.chain.Provider == nil || !fetchAheadEnabled() {
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
