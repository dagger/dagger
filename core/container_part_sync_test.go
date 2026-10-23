package core

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// partSyncTestOp is a refined container op with a group per snapshot part.
// Its mount group holds until released, so a test can run the root
// filesystem's group while the mount's is still running.
type partSyncTestOp struct {
	LazyState
	Parent       dagql.ObjectResult[*Container]
	mountEntered chan struct{}
	mountRelease chan struct{}
	enterOnce    sync.Once
}

var _ LazyContainerParts = (*partSyncTestOp)(nil)

func (op *partSyncTestOp) Evaluate(ctx context.Context, ctr *Container) error {
	return ctr.evaluateAllLazyGroups(ctx, op)
}

func (op *partSyncTestOp) ContainerLazyParent() dagql.ObjectResult[*Container] { return op.Parent }

// Its snapshot groups write their parts rather than copy the parent's, so no
// group consumes another's work as a delegation would.
const (
	partSyncTestFSGroup    dagql.LazyGroupKey = "testFS"
	partSyncTestMountGroup dagql.LazyGroupKey = "testMount"
)

func (op *partSyncTestOp) ContainerLazyGroups(_ context.Context, _ *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if parts == nil {
		return []dagql.LazyGroupKey{ContainerLazyGroupMetadata, partSyncTestFSGroup, partSyncTestMountGroup}, nil
	}
	var groups []dagql.LazyGroupKey
	for _, part := range parts {
		group := partSyncTestFSGroup
		switch part {
		case ContainerPartMetadata:
			group = ContainerLazyGroupMetadata
		case ContainerPartMount("/m"):
			group = partSyncTestMountGroup
		}
		if !slices.Contains(groups, group) {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

func (op *partSyncTestOp) EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error {
	// Each group runs under its latch, as a real op's do.
	return op.LazyState.EvaluateGroup(ctx, "Container.partSyncTest", group, func(ctx context.Context) error {
		switch group {
		case partSyncTestFSGroup:
			ctr.FS.setValue(containerPersistenceTestDirectory("fs-owned", "/"))
		case partSyncTestMountGroup:
			op.enterOnce.Do(func() { close(op.mountEntered) })
			select {
			case <-op.mountRelease:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			ctr.Mounts[0].DirectorySource.setValue(containerPersistenceTestDirectory("mount-owned", "/"))
		}
		return nil
	})
}

func (op *partSyncTestOp) ContainerLazyState() *LazyState { return &op.LazyState }

func (op *partSyncTestOp) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (op *partSyncTestOp) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// A group's owner lease sync covers the parts it filled and doesn't wait for
// the container's other groups: the root filesystem's group completes, with
// its lease, while the mount's group is still running.
func TestContainerGroupLeaseSyncDoesNotWaitForOtherGroups(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "part-lease-sync")
	ctx, cache, srv := env.open(t)
	parent := env.attach(t, ctx, cache, srv, "parent", NewContainer(Platform{})).(dagql.ObjectResult[*Container])
	op := &partSyncTestOp{LazyState: NewLazyState(), Parent: parent, mountEntered: make(chan struct{}), mountRelease: make(chan struct{})}
	release := sync.OnceFunc(func() { close(op.mountRelease) })
	defer release()
	ctr := NewContainer(Platform{})
	ctr.Mounts = []ContainerMount{{Target: "/m", DirectorySource: new(LazyAccessor[*Directory, *Container])}}
	ctr.Lazy = op
	res := env.attach(t, ctx, cache, srv, "ctr", ctr)

	mountDone := make(chan error, 1)
	go func() { mountDone <- cache.EvaluateParts(ctx, res, ContainerPartMount("/m")) }()
	select {
	case <-op.mountEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the mount group didn't start")
	}

	fsDone := make(chan error, 1)
	go func() { fsDone <- cache.EvaluateParts(ctx, res, ContainerPartFS) }()
	select {
	case err := <-fsDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the root filesystem's group waited for the mount's group")
	}
	require.Contains(t, ownedSnapshots(env), "fs-owned", "the filled part has its owner lease")
	require.NotContains(t, ownedSnapshots(env), "mount-owned")

	release()
	require.NoError(t, <-mountDone)
	require.Contains(t, ownedSnapshots(env), "mount-owned", "the mount's own sync leases it")
}

func ownedSnapshots(env *persistedFamiliesTestEnv) []string {
	env.manager.mu.Lock()
	defer env.manager.mu.Unlock()
	var out []string
	for _, id := range env.manager.owners {
		out = append(out, id)
	}
	return out
}
