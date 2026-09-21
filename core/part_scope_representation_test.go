package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

func TestPartScopeNestedNullableRepresentation(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	db := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := containerPersistenceTestCache(t, db, manager, "native")
	file := func(id, path string) *File {
		value := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), stored: &storedSnapshot{SnapshotID: id}, Platform: Platform{OS: "linux", Architecture: "amd64"}}
		value.SetPath(path)
		return value
	}
	values := dagql.Array[dagql.Nullable[dagql.Array[*File]]]{{Valid: true, Value: dagql.Array[*File]{file("same-ref", "/first"), file("same-ref", "/second")}}, {Valid: false}}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "nestedInlineFiles", Type: dagql.NewResultCallType(values.Type())}
	res, err := cache.GetOrInitCall(ctx, "native", srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewResultForCall(values, frame) })
	require.NoError(t, err)
	record, err := cache.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
	require.Len(t, record.SnapshotLinks, 2)
	for i, link := range record.SnapshotLinks {
		require.Equal(t, dagql.PersistedRefPath{}.Field("items").Index(0).Field("items").Index(i), link.OutputPath)
		require.Equal(t, "snapshot", link.Role)
		require.Equal(t, "same-ref", link.RefKey)
	}
	require.Empty(t, manager.opens)
	require.NoError(t, cache.ReleaseSession(ctx, "native"))
	require.NoError(t, cache.Close(ctx))
	manager = newContainerPersistenceTestSnapshots()
	ctx, cache, srv = containerPersistenceTestCache(t, db, manager, "restored")
	require.Equal(t, dagql.CachePersistenceResetNone, cache.PersistenceResetReason())
	res, err = cache.LoadResultByResultID(ctx, "", srv, record.ResultID)
	require.NoError(t, err)
	require.Empty(t, manager.opens)
	outer := res.Unwrap().(dagql.DynamicResultArrayOutput)
	require.Nil(t, outer.Values[1])
	inner, _ := outer.Values[0].DerefValue()
	items := inner.Unwrap().(dagql.DynamicResultArrayOutput).Values
	for i, item := range items {
		value, _ := dagql.UnwrapAs[*File](item)
		require.NotNil(t, value)
		require.Equal(t, "same-ref", value.stored.SnapshotID)
		scope := value.partHost.Load().DecodeContext(ctx).SnapshotScope()
		require.Equal(t, record.ResultID, scope.OwnerResultID)
		require.Equal(t, record.SnapshotLinks[i].OutputPath, scope.OutputPath)
	}
	again, err := cache.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
	require.Equal(t, record.SnapshotLinks, again.SnapshotLinks)
	require.Empty(t, manager.opens)
}
