package snapshots

import (
	"context"
	"errors"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
)

// ErrLiveChangesUnsupported is returned when the snapshotter cannot cheaply
// capture the changes of a live (active, in-use) snapshot, e.g. because it is
// not overlay-based. Callers should fall back to another capture strategy.
var ErrLiveChangesUnsupported = errors.New("capturing live snapshot changes is not supported by this snapshotter")

// LiveChangesSnapshotter is implemented by snapshot managers that can capture
// the current state of a live mutable ref without disturbing its user.
type LiveChangesSnapshotter interface {
	// SnapshotLiveChanges returns a new immutable ref equal to base plus the
	// changes currently held by active, which must have been created on top of
	// base and may still be mounted by a running container. mergedView is a
	// host path to active's merged view as mounted by that container (e.g.
	// /proc/<pid>/root/<dest>). base may be nil (scratch).
	SnapshotLiveChanges(ctx context.Context, base ImmutableRef, active MutableRef, mergedView string, opts ...RefOption) (ImmutableRef, error)
}

var _ LiveChangesSnapshotter = (*snapshotManager)(nil)

func (cm *snapshotManager) SnapshotLiveChanges(ctx context.Context, base ImmutableRef, active MutableRef, mergedView string, opts ...RefOption) (ImmutableRef, error) {
	if active == nil {
		return nil, errors.New("snapshot live changes: active ref is nil")
	}
	msn, ok := cm.Snapshotter.(*mergeSnapshotter)
	if !ok {
		return nil, ErrLiveChangesUnsupported
	}
	var baseKey string
	if base != nil {
		baseKey = base.SnapshotID()
	}

	ctx, err := EnsureLease(ctx)
	if err != nil {
		return nil, err
	}

	id := identity.NewID()
	snapshotID := id
	if err := msn.applyLiveChanges(ctx, snapshotID, baseKey, active.SnapshotID(), mergedView); err != nil {
		return nil, err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(id)
	rec := &cacheRecord{
		mutable: false,
		cm:      cm,
		md:      md,
	}
	opts = append(opts, withSnapshotID(snapshotID))
	if err := initializeMetadata(rec.md, opts...); err != nil {
		return nil, err
	}
	if err := rec.md.queueSnapshotID(snapshotID); err != nil {
		return nil, err
	}
	if err := rec.md.queueCommitted(true); err != nil {
		return nil, err
	}
	if err := rec.md.commitMetadata(); err != nil {
		return nil, err
	}

	cm.records[id] = rec
	ref := &immutableRef{
		cm:              cm,
		refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
		triggerLastUsed: true,
	}
	bklog.G(context.TODO()).WithFields(ref.traceLogFields()).Trace("acquired cache ref")
	return ref, nil
}
