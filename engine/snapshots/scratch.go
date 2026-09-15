package snapshots

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/pkg/errors"
)

const (
	scratchSnapshotID = "dagger-scratch-rootfs-v1"
	scratchLeaseID    = "dagger-scratch-rootfs-v1"
)

func isScratchSnapshotID(snapshotID string) bool {
	return snapshotID == scratchSnapshotID
}

func (cm *snapshotManager) Scratch(ctx context.Context) (ImmutableRef, error) {
	cm.scratchMu.Lock()
	defer cm.scratchMu.Unlock()

	if _, err := cm.Snapshotter.Stat(ctx, scratchSnapshotID); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return nil, errors.Wrap(err, "stat scratch snapshot")
		}
		if err := cm.createScratchSnapshot(ctx); err != nil {
			return nil, err
		}
	}
	if err := cm.AttachLease(ctx, scratchLeaseID, scratchSnapshotID); err != nil {
		return nil, err
	}
	return cm.GetBySnapshotID(ctx, scratchSnapshotID, NoUpdateLastUsed)
}

func (cm *snapshotManager) createScratchSnapshot(ctx context.Context) error {
	if err := cm.ensureScratchLease(ctx); err != nil {
		return err
	}

	key := scratchSnapshotID + "-" + identity.NewID()
	leaseCtx := leases.WithLease(ctx, scratchLeaseID)
	if err := cm.Snapshotter.Prepare(leaseCtx, key, ""); err != nil {
		return errors.Wrap(err, "prepare scratch snapshot")
	}
	active := true
	defer func() {
		if active {
			_ = cm.Snapshotter.Remove(context.WithoutCancel(ctx), key)
		}
	}()

	if err := cm.Snapshotter.Commit(leaseCtx, scratchSnapshotID, key); err != nil {
		if cerrdefs.IsAlreadyExists(err) {
			return nil
		}
		return errors.Wrap(err, "commit scratch snapshot")
	}
	active = false
	return nil
}

func (cm *snapshotManager) ensureScratchLease(ctx context.Context) error {
	_, err := cm.LeaseManager.Create(ctx, func(l *leases.Lease) error {
		l.ID = scratchLeaseID
		l.Labels = map[string]string{
			"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	if err != nil && !cerrdefs.IsAlreadyExists(err) {
		return errors.Wrap(err, "create scratch lease")
	}
	return nil
}
