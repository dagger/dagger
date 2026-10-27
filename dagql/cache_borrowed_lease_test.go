package dagql

import (
	"context"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
)

// borrowSnapshotValue holds one snapshot of its own and one it copied from a
// result it depends on.
type borrowSnapshotValue struct {
	Owned, Borrowed string
}

func (*borrowSnapshotValue) Type() *ast.Type {
	return &ast.Type{NamedType: "BorrowSnapshotValue", NonNull: true}
}

func (v *borrowSnapshotValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	return []PersistedSnapshotRefLink{
		{RefKey: v.Owned, Role: "own"},
		{RefKey: v.Borrowed, Role: "copy", Borrowed: true},
	}
}

// A borrowed snapshot gets no owner lease of the result's own, and so none
// is removed when the result goes, but the result still records it.
func TestCacheSnapshotOwnerLeaseSkipsBorrowed(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	snapshotManager := &fakeSnapshotManager{}
	c, err := NewCache(ctx, "", snapshotManager, nil)
	assert.NilError(t, err)
	sessionID := cacheTestSessionID(t, ctx)

	call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&borrowSnapshotValue{}).Type()),
		Field: "borrowed-owner",
	}
	value := &borrowSnapshotValue{Owned: "snapshot-own", Borrowed: "snapshot-parent"}
	res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(value), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.SyncResultSnapshotOwnerLeases(ctx, res))

	var attached []string
	for _, call := range snapshotManager.attachCalls {
		attached = append(attached, call.SnapshotID)
	}
	assert.DeepEqual(t, attached, []string{"snapshot-own"})

	stored := map[string]bool{}
	for _, link := range res.cacheSharedResult().loadSnapshotOwnerLinks() {
		stored[link.RefKey] = link.Borrowed
	}
	assert.DeepEqual(t, stored, map[string]bool{"snapshot-own": false, "snapshot-parent": true})

	assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	assert.Equal(t, c.Size(), 0)
	for _, id := range snapshotManager.removeCalls {
		assert.Assert(t, !strings.Contains(id, "copy"), "removed a borrowed snapshot's lease: %s", id)
	}
	assert.Equal(t, len(snapshotManager.removeCalls), 1, "removed: %v", snapshotManager.removeCalls)
}
