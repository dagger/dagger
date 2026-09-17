package dagql

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type syncSnapshotOwnerValue struct {
	persistSnapshotValue
	reads int
	err   error
}

func (*syncSnapshotOwnerValue) PersistedOutputRevision() (OutputRevision, error) {
	return 0, ErrPersistStateNotReady
}

func (v *syncSnapshotOwnerValue) ReadSnapshotOwner() (OutputRevision, []PersistedSnapshotRefLink, error) {
	v.reads++
	return 1, v.PersistedSnapshotRefLinks(), v.err
}

func TestSnapshotOwnerSyncReadSelection(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", &fakeSnapshotManager{}, nil)
	require.NoError(t, err)
	session := cacheTestSessionID(t, ctx)
	value := &syncSnapshotOwnerValue{persistSnapshotValue: persistSnapshotValue{SnapshotID: "owned"}}
	call := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(value.Type()), Field: "owner"}
	res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(value), nil
	})
	require.NoError(t, err, "fresh publication uses the blocking read")
	require.Equal(t, 2, value.reads, "read and final revision validation")
	_, err = desiredSnapshotLinksForResult(res.cacheSharedResult(), false)
	require.ErrorIs(t, err, ErrPersistStateNotReady, "boot/import retain the nonblocking collector")
	require.Equal(t, 2, value.reads)
	require.NoError(t, c.SyncResultSnapshotOwnerLeases(ctx, res))
	require.Equal(t, 4, value.reads)
	value.err = errors.New("ownership read failed")
	require.ErrorIs(t, c.SyncResultSnapshotOwnerLeases(ctx, res), value.err, "real errors retain their bookkeeping meaning")
	value.err = nil
	require.NoError(t, c.ReleaseSession(ctx, session))
	require.NoError(t, c.Close(ctx))
}

func TestSnapshotOwnerSyncInlineRead(t *testing.T) {
	value := &syncSnapshotOwnerValue{persistSnapshotValue: persistSnapshotValue{SnapshotID: "owned"}}
	self := DynamicResultArrayOutput{Elem: value, Values: []AnyResult{newDetachedResult(nil, value)}}
	frame := persistCodecFrame("inline", self)
	_, err := collectSnapshotOwnerLinks(self, frame, false)
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	links, err := collectSnapshotOwnerLinks(self, frame, true)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, PersistedRefPath{}.Field("items").Index(0), links[0].OutputPath)
	require.Equal(t, 2, value.reads)
}
