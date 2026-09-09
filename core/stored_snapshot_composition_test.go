package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

func storedSnapshotTestList(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session string, values []dagql.AnyResult) dagql.AnyResult {
	t.Helper()
	list := dagql.DynamicResultArrayOutput{Elem: &Directory{}, Values: values}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "directories", Type: dagql.NewResultCallType(list.Type())}
	res, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(list, frame)
	})
	require.NoError(t, err)
	return res
}

func storedSnapshotTestEnvelope(t *testing.T, ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) string {
	t.Helper()
	encoding, err := dagql.DefaultPersistedSelfCodec.EncodeResult(ctx, cache, res)
	require.NoError(t, err)
	data, err := json.Marshal(encoding.Envelope)
	require.NoError(t, err)
	return string(data)
}

func TestPersistedObjectListSurvivesRepeatedRestore(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := containerPersistenceTestCache(t, db, newContainerPersistenceTestSnapshots(), "a")
	var children []dagql.AnyResult
	var ids []uint64
	for _, name := range []string{"one", "two"} {
		res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", name, storedSnapshotTestValue("Directory", name, "/"+name, false), false)
		id, err := cache.PersistedResultID(res)
		require.NoError(t, err)
		ids = append(ids, id)
		children = append(children, res)
	}
	list := storedSnapshotTestList(t, ctx, cache, srv, "a", children)
	listID, err := cache.PersistedResultID(list)
	require.NoError(t, err)
	original := storedSnapshotTestEnvelope(t, ctx, cache, list)
	require.NoError(t, cache.ReleaseSession(ctx, "a"))
	require.NoError(t, cache.Close(ctx))
	for _, session := range []string{"b", "c", "d"} {
		manager := newContainerPersistenceTestSnapshots()
		ctx, cache, srv = containerPersistenceTestCache(t, db, manager, session)
		list, err = cache.LoadResultByResultID(ctx, session, srv, listID)
		require.NoError(t, err)
		// Inspect the stored slots without calling NthValue in the middle cache.
		children = list.Unwrap().(dagql.DynamicResultArrayOutput).Values
		for i, child := range children {
			id, err := cache.PersistedResultID(child)
			require.NoError(t, err)
			require.Equal(t, ids[i], id)
			_, open := storedSnapshotTestOpen(child)
			require.False(t, open)
		}
		require.Equal(t, original, storedSnapshotTestEnvelope(t, ctx, cache, list))
		require.Empty(t, manager.opens)
		if session == "c" {
			dagql.Fields[*Query]{
				dagql.NodeFunc("selectFirst", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Directory], error) {
					child, err := list.NthValue(ctx, 1)
					if err != nil {
						return dagql.ObjectResult[*Directory]{}, err
					}
					return child.(dagql.ObjectResult[*Directory]), nil
				}),
			}.Install(srv)
			var child dagql.ObjectResult[*Directory]
			require.NoError(t, srv.Select(ctx, srv.Root(), &child, dagql.Selector{Field: "selectFirst"}))
			direct, err := cache.LoadResultByResultID(ctx, "", srv, ids[0])
			require.NoError(t, err)
			require.Same(t, child.Unwrap(), direct.Unwrap())
			require.NoError(t, cache.Evaluate(ctx, child))
			require.NoError(t, cache.Evaluate(ctx, direct))
			require.Equal(t, 1, manager.openCount("one"))
			require.Zero(t, manager.openCount("two"))
		}
		require.NoError(t, cache.ReleaseSession(ctx, session))
		require.NoError(t, cache.Close(ctx))
	}
}

func TestWorkspaceRestoreOpensNothing(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := containerPersistenceTestCache(t, db, newContainerPersistenceTestSnapshots(), "a")
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Workspace]{}))
	root := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "root", storedSnapshotTestValue("Directory", "root", "/", false), false).(dagql.ObjectResult[*Directory])
	source := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "source", storedSnapshotTestValue("Directory", "source", "/sub", false), false).(dagql.ObjectResult[*Directory])
	res := attachTransferObject(t, ctx, cache, srv, "a", "workspace", &Workspace{rootfs: root, source: NewWorkspaceSourceDirectory(source)})
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	original := storedSnapshotTestEnvelope(t, ctx, cache, res)
	require.NoError(t, cache.ReleaseSession(ctx, "a"))
	require.NoError(t, cache.Close(ctx))
	for _, session := range []string{"b", "c"} {
		manager := newContainerPersistenceTestSnapshots()
		ctx, cache, srv = containerPersistenceTestCache(t, db, manager, session)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Workspace]{}))
		loaded, err := cache.LoadResultByResultID(ctx, session, srv, id)
		require.NoError(t, err)
		ws := loaded.Unwrap().(*Workspace)
		require.NotNil(t, ws.rootfs.Self().stored)
		require.NotNil(t, ws.source.(*WorkspaceSourceDirectory).Root.Self().stored)
		require.Equal(t, original, storedSnapshotTestEnvelope(t, ctx, cache, loaded))
		require.Empty(t, manager.opens)
		require.NoError(t, cache.ReleaseSession(ctx, session))
		require.NoError(t, cache.Close(ctx))
	}
}

func TestModuleObjectListFieldRestore(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := containerPersistenceTestCache(t, db, newContainerPersistenceTestSnapshots(), "a")
	install := func(srv *dagql.Server) *ModuleObject {
		installModuleObjectTestModuleClass(srv)
		module := &Module{NameField: "test", Deps: NewSchemaBuilder(nil, nil)}
		mod, err := dagql.NewObjectResultForCall(module, srv, moduleObjectTestSyntheticCall("list-module", module))
		require.NoError(t, err)
		shape := &ModuleObject{Module: mod, TypeDef: NewObjectTypeDef("ListHolder", "", nil)}
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: shape}))
		return shape
	}
	shape := install(srv)
	child := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "child", storedSnapshotTestValue("Directory", "child", "", false), false)
	childID, err := cache.PersistedResultID(child)
	require.NoError(t, err)
	list := storedSnapshotTestList(t, ctx, cache, srv, "a", []dagql.AnyResult{child})
	listID, err := cache.PersistedResultID(list)
	require.NoError(t, err)
	shape.Fields = map[string]any{"array": []any{child}, "list": list}
	res := attachTransferObject(t, ctx, cache, srv, "a", "holder", shape)
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	original := storedSnapshotTestEnvelope(t, ctx, cache, res)
	require.NoError(t, cache.ReleaseSession(ctx, "a"))
	require.NoError(t, cache.Close(ctx))
	for _, session := range []string{"b", "c"} {
		manager := newContainerPersistenceTestSnapshots()
		ctx, cache, srv = containerPersistenceTestCache(t, db, manager, session)
		install(srv)
		loaded, err := cache.LoadResultByResultID(ctx, session, srv, id)
		require.NoError(t, err)
		fields := loaded.Unwrap().(*ModuleObject).Fields
		fromArray := fields["array"].([]any)[0].(dagql.AnyResult)
		fromList := fields["list"].(dagql.AnyResult)
		got, err := cache.PersistedResultID(fromList)
		require.NoError(t, err)
		require.Equal(t, listID, got)
		listChild := fromList.Unwrap().(dagql.DynamicResultArrayOutput).Values[0]
		for _, value := range []dagql.AnyResult{fromArray, listChild} {
			got, err := cache.PersistedResultID(value)
			require.NoError(t, err)
			require.Equal(t, childID, got)
			_, open := storedSnapshotTestOpen(value)
			require.False(t, open)
		}
		require.Same(t, fromArray.Unwrap(), listChild.Unwrap())
		require.Equal(t, original, storedSnapshotTestEnvelope(t, ctx, cache, loaded))
		require.Empty(t, manager.opens)
		require.NoError(t, cache.ReleaseSession(ctx, session))
		require.NoError(t, cache.Close(ctx))
	}
}

func TestRestoredSnapshotLazyChildrenKeepParentClosed(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		t.Run(kind, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "cache.db")
			ctx, cache, srv := containerPersistenceTestCache(t, db, newContainerPersistenceTestSnapshots(), "a")
			parent := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "parent", storedSnapshotTestValue(kind, "parent", "/source", false), false)
			var child dagql.Typed
			if kind == "Directory" {
				child = &Directory{Platform: Platform{OS: "linux", Architecture: "arm64"}, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Lazy: &DirectoryWithoutLazy{LazyState: NewLazyState(), Parent: parent.(dagql.ObjectResult[*Directory]), Paths: []string{"absent"}}}
			} else {
				child = &File{Platform: Platform{OS: "linux", Architecture: "arm64"}, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Lazy: &FileWithNameLazy{LazyState: NewLazyState(), Parent: parent.(dagql.ObjectResult[*File]), Filename: "renamed"}}
			}
			res := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "child", child, true)
			id, err := cache.PersistedResultID(res)
			require.NoError(t, err)
			require.NoError(t, cache.ReleaseSession(ctx, "a"))
			require.NoError(t, cache.Close(ctx))
			manager := newContainerPersistenceTestSnapshots()
			ctx, cache, srv = containerPersistenceTestCache(t, db, manager, "b")
			res, err = cache.LoadResultByResultID(ctx, "b", srv, id)
			require.NoError(t, err)
			require.Zero(t, manager.openCount("parent"))
			require.True(t, dagql.HasPendingLazyComputation(res))
			// The fake store stops at mutable allocation, after the parent demand.
			require.ErrorIs(t, cache.Evaluate(ctx, res), context.Canceled)
			require.Equal(t, 1, manager.openCount("parent"))
			require.Len(t, manager.newCalls, 1)
		})
	}
}

func TestRestoredSnapshotContainerRecipeInput(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := containerPersistenceTestCache(t, db, newContainerPersistenceTestSnapshots(), "a")
	platform := Platform{OS: "linux", Architecture: "arm64"}
	parent := NewContainer(platform)
	parent.FS.setValue(containerPersistenceTestDirectory("rootfs", "/"))
	parentRes := attachTransferObject(t, ctx, cache, srv, "a", "parent", parent)
	source := attachStoredSnapshotTestValue(t, ctx, cache, srv, "a", "source", storedSnapshotTestValue("Directory", "source", "/", false), false).(dagql.ObjectResult[*Directory])
	child := NewContainer(platform)
	child.Lazy = &ContainerWithDirectoryLazy{LazyState: NewLazyState(), Parent: parentRes, Path: "/dest", Source: source}
	res := attachTransferObject(t, ctx, cache, srv, "a", "withDirectory", child)
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "a"))
	require.NoError(t, cache.Close(ctx))
	manager := newContainerPersistenceTestSnapshots()
	ctx, cache, srv = containerPersistenceTestCache(t, db, manager, "b")
	loaded, err := cache.LoadResultByResultID(ctx, "b", srv, id)
	require.NoError(t, err)
	require.Zero(t, manager.openCount("source"))
	dagql.Fields[*Container]{
		dagql.NodeFunc("rootfs", func(ctx context.Context, parent dagql.ObjectResult[*Container], _ struct{}) (dagql.ObjectResult[*Directory], error) {
			dir := &Directory{Platform: parent.Self().Platform, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Lazy: &ContainerRootFSLazy{LazyState: NewLazyState(), Parent: parent}}
			dir.Dir.setValue("/")
			return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
		}),
	}.Install(srv)
	dagql.Fields[*Query]{
		dagql.Func("demand", func(ctx context.Context, _ *Query, _ struct{}) (dagql.Boolean, error) {
			return false, cache.EvaluateParts(ctx, loaded, ContainerPartFS)
		}),
	}.Install(srv)
	var out dagql.Boolean
	// The fake store stops after demanding the source, at mutable allocation.
	require.ErrorIs(t, srv.Select(ctx, srv.Root(), &out, dagql.Selector{Field: "demand"}), context.Canceled)
	require.Equal(t, 1, manager.openCount("source"))
	require.Len(t, manager.newCalls, 1)
}
