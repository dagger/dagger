package snapshots

import (
	"context"
	"fmt"
)

// ImportChain imports one immutable chain from an in-process provider. Snapshot
// reuse precedes byte access. The returned ref owns its resources until Release,
// giving an ordinary caller time to attach its own snapshot lease.
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
	// ahead holds the download of the next layer's blob, started while this
	// layer's applies.
	var ahead *contentFetch
	defer func() {
		if rerr != nil {
			cm.dropFetch(ctx, ahead)
			if current != nil {
				_ = current.Release(context.WithoutCancel(ctx))
			}
			_ = pin.release(ctx)
		}
	}()
	for i, layer := range chain.Layers {
		held := ahead
		ahead = nil
		layerImport := &chainLayer{owner: chain, ahead: held}
		if chain.Provider != nil && i+1 < len(chain.Layers) && fetchAheadEnabled() {
			// Once this layer's blob is in, fetch the next one while this
			// one applies.
			layerImport.fetched = func() {
				var started bool
				ahead, started = cm.fetchAhead(ctx, chain.Layers[i+1].Descriptor, chain.Provider, chain)
				if started && chain.Timing != nil {
					chain.Timing.Ahead++
				}
			}
		}
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
