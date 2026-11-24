package snapshots

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// PrefetchContent downloads desc's blob from provider into the content store
// before any import needs it, joining a download of it already in progress.
// A new lease holds the blob until release is called, or until ttl passes,
// and an engine start drops it with the other transfer leases. A blob
// already in the store is neither downloaded nor held: fetched is false and
// release is nil.
func (cm *snapshotManager) PrefetchContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, ttl time.Duration) (release func(context.Context) error, fetched bool, rerr error) {
	if _, err := cm.ContentStore.Info(ctx, desc.Digest); err == nil {
		return nil, false, nil
	}
	l, err := cm.LeaseManager.Create(ctx, leases.WithRandomID(), leases.WithExpiration(ttl), leases.WithLabels(map[string]string{snapshotTransferLeaseLabel: "true"}))
	if err != nil {
		return nil, false, err
	}
	release = func(ctx context.Context) error {
		return cm.LeaseManager.Delete(ctx, l)
	}
	if _, _, err := cm.fetchChainContent(leases.WithLease(ctx, l.ID), desc, provider, nil, nil); err != nil {
		_ = release(context.WithoutCancel(ctx))
		return nil, false, err
	}
	return release, true, nil
}

// HoldContent holds desc's blob, already in the content store, as
// PrefetchContent holds one it downloads: under a new lease, released by
// release, by ttl, or by an engine start. A blob not in the store is not
// held: held is false and release is nil.
func (cm *snapshotManager) HoldContent(ctx context.Context, desc ocispecs.Descriptor, ttl time.Duration) (release func(context.Context) error, held bool, rerr error) {
	l, err := cm.LeaseManager.Create(ctx, leases.WithRandomID(), leases.WithExpiration(ttl), leases.WithLabels(map[string]string{snapshotTransferLeaseLabel: "true"}))
	if err != nil {
		return nil, false, err
	}
	release = func(ctx context.Context) error {
		return cm.LeaseManager.Delete(ctx, l)
	}
	present, err := cm.pinContent(leases.WithLease(ctx, l.ID), desc)
	if err != nil || !present {
		_ = release(context.WithoutCancel(ctx))
		return nil, false, err
	}
	return release, true, nil
}
