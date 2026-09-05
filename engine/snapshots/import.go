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
	defer func() {
		if rerr != nil {
			if current != nil {
				_ = current.Release(context.WithoutCancel(ctx))
			}
			_ = pin.release(ctx)
		}
	}()
	for _, layer := range chain.Layers {
		next, err := cm.importLayer(ctx, layer.Descriptor, current, chain.Provider, ImportImageOpts{})
		if err != nil {
			return nil, err
		}
		if current != nil {
			_ = current.Release(context.WithoutCancel(ctx))
		}
		current = next
	}
	current.(*immutableRef).pin = pin
	return current, nil
}
