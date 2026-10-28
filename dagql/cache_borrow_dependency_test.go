package dagql

import (
	"context"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
)

// selectSnapshotValue holds the snapshot of the result it was selected from,
// which it depends on, as a container's rootfs or a subdirectory does.
type selectSnapshotValue struct {
	SnapshotID string
	From       AnyResult
}

func (*selectSnapshotValue) Type() *ast.Type {
	return &ast.Type{NamedType: "SelectSnapshotValue", NonNull: true}
}

func (v *selectSnapshotValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	return []PersistedSnapshotRefLink{{RefKey: v.SnapshotID, Role: "snapshot"}}
}

func (v *selectSnapshotValue) AttachDependencyResults(_ context.Context, _ AnyResult, attach func(AnyResult) (AnyResult, error)) ([]AnyResult, error) {
	attached, err := attach(v.From)
	if err != nil {
		return nil, err
	}
	v.From = attached
	return []AnyResult{attached}, nil
}

// A result holding a snapshot that a result it depends on already holds
// borrows it: it writes no owner lease of its own, and the snapshot keeps the
// one lease its dependency holds while either is alive.
func TestCacheSnapshotOwnerLeaseBorrowsADependencysSnapshot(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	snapshotManager := &fakeSnapshotManager{}
	c, err := NewCache(ctx, "", snapshotManager, nil)
	assert.NilError(t, err)
	sessionID := cacheTestSessionID(t, ctx)

	from, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistSnapshotValue{}).Type()),
		Field: "select-from",
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(&persistSnapshotValue{Name: "from", SnapshotID: "snapshot-x"}), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.SyncResultSnapshotOwnerLeases(ctx, from))

	selected, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&selectSnapshotValue{}).Type()),
		Field: "select",
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(&selectSnapshotValue{SnapshotID: "snapshot-x", From: from}), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.SyncResultSnapshotOwnerLeases(ctx, selected))

	var leases []string
	for _, call := range snapshotManager.attachCalls {
		assert.Equal(t, call.SnapshotID, "snapshot-x")
		leases = append(leases, call.LeaseID)
	}
	assert.DeepEqual(t, leases, []string{resultSnapshotLeaseID(from.cacheSharedResult().id, "snapshot")})
	links := selected.cacheSharedResult().loadSnapshotOwnerLinks()
	assert.Equal(t, len(links), 1)
	assert.Assert(t, links[0].Borrowed, "the selection owns the snapshot too")

	assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	assert.Equal(t, c.Size(), 0)
	assert.DeepEqual(t, snapshotManager.removeCalls, []string{resultSnapshotLeaseID(from.cacheSharedResult().id, "snapshot")})
}
