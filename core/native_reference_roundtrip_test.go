package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// TestNativeReferenceVisitorRoundTrip exercises the newly inventoried visitor
// positions through relocation and a real save/reopen. Children start detached:
// no independent persistable call can hide a missing payload ownership edge.
func TestNativeReferenceVisitorRoundTrip(t *testing.T) {
	t.Run("artifact collection roots and views", func(t *testing.T) {
		env := newPersistedFamiliesTestEnv(t, "native-artifact-refs")
		ctx, cache, srv := env.open(t)
		workspace := nativeReferenceDetached(t, srv, "root-workspace", &Workspace{Address: "file:///root", Cwd: "/"}).(dagql.ObjectResult[*Workspace])
		contextWorkspace := nativeReferenceDetached(t, srv, "root-context-workspace", &Workspace{Address: "file:///context", Cwd: "/"}).(dagql.ObjectResult[*Workspace])
		entryView := nativeReferenceDetached(t, srv, "entry-view", storedSnapshotTestValue("Directory", "native-entry-view", "/entry", false)).(dagql.ObjectResult[*Directory])
		rootView := nativeReferenceDetached(t, srv, "root-view", storedSnapshotTestValue("Directory", "native-root-view", "/root", false)).(dagql.ObjectResult[*Directory])
		parent := env.attach(t, ctx, cache, srv, "native-artifact-parent", &Artifacts{
			Entries: []*Artifact{{
				Node: &ModTreeNode{Name: "entry"},
				View: &ArtifactView{sources: []dagql.AnyObjectResult{entryView}},
			}},
			CollectionRoots: []*Artifact{{
				Node:             &ModTreeNode{Name: "root"},
				Workspace:        workspace,
				ContextWorkspace: contextWorkspace,
				View:             &ArtifactView{sources: []dagql.AnyObjectResult{rootView}},
			}},
		})
		attached := parent.Unwrap().(*Artifacts)
		workspaceID := persistedRowID(t, cache, attached.CollectionRoots[0].Workspace)
		contextID := persistedRowID(t, cache, attached.CollectionRoots[0].ContextWorkspace)
		entryViewID := persistedRowID(t, cache, attached.Entries[0].View.sources[0])
		rootViewID := persistedRowID(t, cache, attached.CollectionRoots[0].View.sources[0])
		require.NotEqual(t, workspaceID, contextID)
		require.NotEqual(t, entryViewID, rootViewID)
		expected := map[string]uint64{
			"objectJSON.Entries[0].View.Sources[0]":          entryViewID,
			"objectJSON.CollectionRoots[0].Workspace":        workspaceID,
			"objectJSON.CollectionRoots[0].ContextWorkspace": contextID,
			"objectJSON.CollectionRoots[0].View.Sources[0]":  rootViewID,
		}
		require.Equal(t, expected, assertPersistedRefsMatchOwnership(t, ctx, cache, parent))

		// Swap existing rows of the same type. An omitted rewrite could still
		// decode, but would resolve to the wrong workspace or directory.
		record := coreRelocationRecord(t, ctx, cache, parent)
		before := recordJSON(t, record)
		visitor := &relocationVisitor{mapping: map[uint64]uint64{
			record.ResultID: record.ResultID,
			workspaceID:     contextID, contextID: workspaceID,
			entryViewID: rootViewID, rootViewID: entryViewID,
		}}
		rewritten, err := dagql.VisitEncodedReferences(record, visitor.visit)
		require.NoError(t, err)
		require.Equal(t, expected, visitor.childIDs())
		require.Equal(t, before, recordJSON(t, record), "relocation must not mutate its input")
		decoded, err := (&Artifacts{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, rewritten.ResultID, rewritten.Call), rewritten.Envelope.ObjectJSON)
		require.NoError(t, err)
		relocated := env.attach(t, ctx, cache, srv, "native-artifact-relocated", decoded)
		relocatedExpected := map[string]uint64{}
		for path, id := range expected {
			relocatedExpected[path] = visitor.mapping[id]
		}
		require.Equal(t, relocatedExpected, assertPersistedRefsMatchOwnership(t, ctx, cache, relocated))
		parentID, relocatedID := persistedRowID(t, cache, parent), persistedRowID(t, cache, relocated)

		// restart releases the producer session before saving and importing.
		// The child rows have no independent retained call or tree reference.
		ctx, cache, srv = env.restart(t, ctx, cache)
		for _, fixture := range []struct {
			id        uint64
			refs      map[string]uint64
			workspace string
			context   string
			entryPath string
			rootPath  string
		}{
			{parentID, expected, "file:///root", "file:///context", "/entry", "/root"},
			{relocatedID, relocatedExpected, "file:///context", "file:///root", "/root", "/entry"},
		} {
			loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, fixture.id)
			require.NoError(t, err)
			require.NotNil(t, loaded)
			require.Equal(t, fixture.id, persistedRowID(t, cache, loaded))
			require.Equal(t, fixture.refs, assertPersistedRefsMatchOwnership(t, ctx, cache, loaded))
			restored, ok := loaded.Unwrap().(*Artifacts)
			require.True(t, ok)
			require.Len(t, restored.Entries, 1)
			require.Len(t, restored.CollectionRoots, 1)
			require.Equal(t, fixture.workspace, restored.CollectionRoots[0].Workspace.Self().Address)
			require.Equal(t, fixture.context, restored.CollectionRoots[0].ContextWorkspace.Self().Address)
			entryPath, err := storedSnapshotTestPath(ctx, restored.Entries[0].View.sources[0])
			require.NoError(t, err)
			require.Equal(t, fixture.entryPath, entryPath)
			rootPath, err := storedSnapshotTestPath(ctx, restored.CollectionRoots[0].View.sources[0])
			require.NoError(t, err)
			require.Equal(t, fixture.rootPath, rootPath)
		}
	})

	t.Run("directory source origin git", func(t *testing.T) {
		env := newPersistedFamiliesTestEnv(t, "native-origin-git-refs")
		ctx, cache, srv := env.open(t)
		attachSource := func(name, path string) dagql.AnyResult {
			directory := nativeReferenceDetached(t, srv, name+"-directory", storedSnapshotTestValue("Directory", name+"-snapshot", path, false)).(dagql.ObjectResult[*Directory])
			repository := nativeReferenceDetached(t, srv, name+"-repository", &GitRepository{
				Backend: &LocalGitRepository{Directory: directory}, Remote: &gitutil.Remote{},
			}).(dagql.ObjectResult[*GitRepository])
			return env.attach(t, ctx, cache, srv, name, &ModuleSource{
				Kind: ModuleSourceKindDir, DirSrc: &DirModuleSource{OriginGit: repository},
			})
		}
		left, right := attachSource("origin-left", "/left"), attachSource("origin-right", "/right")
		leftGitID := persistedRowID(t, cache, left.Unwrap().(*ModuleSource).DirSrc.OriginGit)
		rightGitID := persistedRowID(t, cache, right.Unwrap().(*ModuleSource).DirSrc.OriginGit)
		require.NotEqual(t, leftGitID, rightGitID)
		const path = "objectJSON.dirSrc.originGitResultID"
		require.Equal(t, map[string]uint64{path: leftGitID}, assertPersistedRefsMatchOwnership(t, ctx, cache, left))
		require.Equal(t, map[string]uint64{path: rightGitID}, assertPersistedRefsMatchOwnership(t, ctx, cache, right))
		record := coreRelocationRecord(t, ctx, cache, left)
		before := recordJSON(t, record)
		visitor := &relocationVisitor{mapping: map[uint64]uint64{
			record.ResultID: record.ResultID, leftGitID: rightGitID,
		}}
		rewritten, err := dagql.VisitEncodedReferences(record, visitor.visit)
		require.NoError(t, err)
		require.Equal(t, map[string]uint64{path: leftGitID}, visitor.childIDs())
		require.Equal(t, before, recordJSON(t, record))
		decoded, err := (&ModuleSource{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, rewritten.ResultID, rewritten.Call), rewritten.Envelope.ObjectJSON)
		require.NoError(t, err)
		relocated := env.attach(t, ctx, cache, srv, "origin-relocated", decoded)
		leftID, relocatedID := persistedRowID(t, cache, left), persistedRowID(t, cache, relocated)
		ctx, cache, srv = env.restart(t, ctx, cache)
		for _, fixture := range []struct {
			id    uint64
			gitID uint64
			dir   string
		}{{leftID, leftGitID, "/left"}, {relocatedID, rightGitID, "/right"}} {
			loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, fixture.id)
			require.NoError(t, err)
			require.NotNil(t, loaded)
			require.Equal(t, fixture.id, persistedRowID(t, cache, loaded))
			require.Equal(t, map[string]uint64{path: fixture.gitID}, assertPersistedRefsMatchOwnership(t, ctx, cache, loaded))
			restored, ok := loaded.Unwrap().(*ModuleSource)
			require.True(t, ok)
			require.NotNil(t, restored.DirSrc)
			require.Equal(t, fixture.gitID, persistedRowID(t, cache, restored.DirSrc.OriginGit))
			backend, ok := restored.DirSrc.OriginGit.Self().Backend.(*LocalGitRepository)
			require.True(t, ok)
			dir, err := storedSnapshotTestPath(ctx, backend.Directory)
			require.NoError(t, err)
			require.Equal(t, fixture.dir, dir)
		}
	})
}

func nativeReferenceDetached(t *testing.T, srv *dagql.Server, field string, value dagql.Typed) dagql.AnyResult {
	t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	result, err := dagql.NewResultForCall(value, frame)
	require.NoError(t, err)
	class, ok := srv.ObjectType(value.Type().Name())
	require.True(t, ok)
	object, err := class.New(result)
	require.NoError(t, err)
	return object
}
