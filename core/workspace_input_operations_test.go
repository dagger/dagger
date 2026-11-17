package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func workspaceOperationTestServer(t *testing.T) (context.Context, *dagql.Server, *dagql.Cache) {
	t.Helper()
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "operation-client", SessionID: "operation-session"})
	srv.ObserveCalls(ObserveWorkspaceInputs)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*WorkspaceSDK]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*WorkspaceModule]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleSource]{}))
	dagql.Fields[*Query]{
		dagql.NodeFunc("currentWorkspace", func(ctx context.Context, _ dagql.ObjectResult[*Query], args struct{ Owner string }) (dagql.ObjectResult[*Workspace], error) {
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &Workspace{Address: "file:///" + args.Owner, ClientID: "operation-client", Cwd: "."})
		}),
	}.Install(srv)
	return ctx, srv, cache
}

func workspaceOperationFact(t *testing.T, ctx context.Context, cache *dagql.Cache, result dagql.AnyResult) workspaceInputFact {
	t.Helper()
	id, err := result.ID()
	require.NoError(t, err)
	facts, complete, err := cache.ResultFacts(ctx, "operation-session", &dagql.ResultCallRef{ResultID: id.EngineResultID()}, workspaceInputsNamespace)
	require.NoError(t, err)
	require.True(t, complete)
	require.Len(t, facts, 1)
	var fact workspaceInputFact
	require.NoError(t, json.Unmarshal(facts[0].Payload, &fact))
	return fact
}

func TestWorkspaceInputOperationCapture(t *testing.T) {
	ctx, srv, cache := workspaceOperationTestServer(t)
	bodies := 0
	dagql.Fields[*Workspace]{
		dagql.NodeFunc("__withUserConfigOverlay", func(ctx context.Context, parent dagql.ObjectResult[*Workspace], _ struct{}) (dagql.ObjectResult[*Workspace], error) {
			return dagql.NewObjectResultForCurrentCall(ctx, srv, parent.Self().Clone())
		}),
		dagql.Func("glob", func(context.Context, *Workspace, struct{ Pattern string }) (dagql.Array[dagql.String], error) {
			return nil, nil
		}),
		dagql.NodeFunc("directory", func(ctx context.Context, _ dagql.ObjectResult[*Workspace], _ struct {
			Path    string
			Include []string
		}) (dagql.ObjectResult[*Directory], error) {
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
		}),
		dagql.Func("findUp", func(context.Context, *Workspace, struct{ Name, From string }) (dagql.Nullable[dagql.String], error) {
			return dagql.Null[dagql.String](), nil
		}),
		dagql.NodeFunc("moduleSource", func(ctx context.Context, ws dagql.ObjectResult[*Workspace], args struct{ Path string }) (dagql.Result[dagql.String], error) {
			bodies++
			if args.Path == "full" {
				RecordWorkspaceRead(ctx, ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "Workspace.moduleSource"})
				return dagql.NewResultForCurrentCall(ctx, dagql.String("module"))
			}
			// Discarded query results must still form the compound's reads.
			var files dagql.Array[dagql.String]
			if err := srv.Select(ctx, ws, &files, dagql.Selector{Field: "glob", Args: []dagql.NamedInput{{Name: "pattern", Value: dagql.String("mod/dagger.json")}}}); err != nil {
				return dagql.Result[dagql.String]{}, err
			}
			var directory dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, ws, &directory, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("/")}, {Name: "include", Value: dagql.ArrayInput[dagql.String]{"mod/src/**"}}}}); err != nil {
				return dagql.Result[dagql.String]{}, err
			}
			return dagql.NewResultForCurrentCall(ctx, dagql.String("module"))
		}),
		dagql.Func("configRead", func(context.Context, *Workspace, struct{}) (dagql.String, error) { return "config", nil }),
		dagql.Func("futureRead", func(context.Context, *Workspace, struct{}) (dagql.String, error) { return "unknown", nil }),
	}.Install(srv)
	var ws dagql.ObjectResult[*Workspace]
	require.NoError(t, srv.Select(ctx, srv.Root(), &ws, dagql.Selector{Field: "currentWorkspace", Args: []dagql.NamedInput{{Name: "owner", Value: dagql.String("A")}}}))
	boundary, err := workspaceResultBoundary(ctx, ws)
	require.NoError(t, err)
	var view dagql.ObjectResult[*Workspace]
	require.NoError(t, srv.Select(ctx, ws, &view, dagql.Selector{Field: "__withUserConfigOverlay"}))
	viewBoundary, err := workspaceResultBoundary(ctx, view)
	require.NoError(t, err)
	require.Equal(t, boundary, viewBoundary, "a metadata clone keeps the receiver's source identity")
	viewFact := workspaceOperationFact(t, ctx, cache, view)
	require.True(t, viewFact.Complete)
	require.Empty(t, viewFact.Reads)

	for i := 0; i < 2; i++ {
		var result dagql.Result[dagql.String]
		require.NoError(t, srv.Select(ctx, ws, &result, dagql.Selector{Field: "moduleSource", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("mod")}}}))
		fact := workspaceOperationFact(t, ctx, cache, result)
		require.True(t, fact.Complete)
		require.Empty(t, fact.Issues)
		frame, err := result.ResultCall()
		require.NoError(t, err)
		collector, err := newWorkspaceInputCollector(ctx)
		require.NoError(t, err)
		selectors, issues := collector.materializeReads(fact, frame).forWorkspace(boundary)
		require.Empty(t, issues)
		for _, path := range []string{"/mod/dagger.json", "/mod/src/main.go"} {
			match, err := provenance.AffectedByFile(selectors, path, nil)
			require.NoError(t, err)
			require.True(t, match, path)
		}
		match, err := provenance.AffectedByFile(selectors, "/README.md", nil)
		require.NoError(t, err)
		require.False(t, match, "compound capture must not invent a workspace-wide read")
	}
	require.Equal(t, 1, bodies, "exact cache reuse must preserve discarded nested reads")
	var full dagql.Result[dagql.String]
	require.NoError(t, srv.Select(ctx, ws, &full, dagql.Selector{Field: "moduleSource", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("full")}}}))
	fullFact := workspaceOperationFact(t, ctx, cache, full)
	require.True(t, fullFact.Complete)
	require.Len(t, fullFact.Reads, 1)
	require.Equal(t, "/", fullFact.Reads[0].Source.Selectors[0].Path)

	var unknown dagql.Result[dagql.String]
	require.NoError(t, srv.Select(ctx, ws, &unknown, dagql.Selector{Field: "futureRead"}))
	unknownFact := workspaceOperationFact(t, ctx, cache, unknown)
	require.False(t, unknownFact.Complete)
	require.Contains(t, unknownFact.Prepared.Issues, "Workspace.futureRead has unclassified source effects")
	var config dagql.Result[dagql.String]
	require.NoError(t, srv.Select(ctx, ws, &config, dagql.Selector{Field: "configRead"}))
	configFact := workspaceOperationFact(t, ctx, cache, config)
	require.True(t, configFact.Complete)
	require.NotEmpty(t, configFact.Reads, "direct I/O must remain a source read")
}

func TestWorkspaceInputRetainedCapabilities(t *testing.T) {
	ctx, srv, cache := workspaceOperationTestServer(t)
	dagql.Fields[*Workspace]{
		dagql.NodeFunc("__workspaceSDK", func(ctx context.Context, parent dagql.ObjectResult[*Workspace], _ struct{}) (dagql.ObjectResult[*WorkspaceSDK], error) {
			result, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &WorkspaceSDK{Workspace: parent, Name: "same"})
			if err != nil {
				return result, err
			}
			return result.WithContentDigest(ctx, digest.FromString("equal SDK metadata"))
		}),
		dagql.NodeFunc("moduleSource", func(ctx context.Context, parent dagql.ObjectResult[*Workspace], _ struct{}) (dagql.ObjectResult[*ModuleSource], error) {
			result, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &ModuleSource{Workspace: parent, Kind: ModuleSourceKindDir, SourceRootSubpath: "."})
			if err != nil {
				return result, err
			}
			return result.WithContentDigest(ctx, digest.FromString("equal module source metadata"))
		}),
		dagql.NodeFunc("__workspaceModule", func(ctx context.Context, _ dagql.ObjectResult[*Workspace], _ struct{}) (dagql.ObjectResult[*WorkspaceModule], error) {
			result, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &WorkspaceModule{Name: "same", Source: "mod"})
			if err != nil {
				return result, err
			}
			return result.WithContentDigest(ctx, digest.FromString("equal module metadata"))
		}),
	}.Install(srv)
	var owners [2]dagql.ObjectResult[*Workspace]
	var boundaries [2]string
	for i, name := range []string{"A", "B"} {
		require.NoError(t, srv.Select(ctx, srv.Root(), &owners[i], dagql.Selector{Field: "currentWorkspace", Args: []dagql.NamedInput{{Name: "owner", Value: dagql.String(name)}}}))
		var err error
		boundaries[i], err = workspaceResultBoundary(ctx, owners[i])
		require.NoError(t, err)
	}
	for _, field := range []string{"__workspaceSDK", "__workspaceModule", "moduleSource"} {
		t.Run(field, func(t *testing.T) {
			var firstPayload dagql.Typed
			for i, owner := range owners {
				for repeat := 0; repeat < 2; repeat++ {
					result, err := owner.Select(ctx, srv, dagql.Selector{Field: field})
					require.NoError(t, err)
					if firstPayload == nil {
						firstPayload = result.Unwrap()
					} else if i == 1 {
						require.Same(t, firstPayload, result.Unwrap(), "the test must exercise a borrowed equal payload")
					}
					fact := workspaceOperationFact(t, ctx, cache, result)
					require.True(t, fact.Complete)
					require.True(t, fact.CapabilitiesComplete)
					require.Empty(t, fact.Reads, "constructing metadata does not read source files")
					require.Len(t, fact.Retained, 1)
					require.Equal(t, boundaries[i], fact.Retained[0].Workspace)
					collector, err := newWorkspaceInputCollector(ctx)
					require.NoError(t, err)
					collector.retainedValue(result)
					selectors, issues := collector.result.forWorkspace(boundaries[i])
					require.Empty(t, issues)
					match, err := provenance.AffectedByFile(selectors, "/README.md", nil)
					require.NoError(t, err)
					require.True(t, match, "future use still has the entire Workspace capability")
					if i == 1 {
						selectors, issues = collector.result.forWorkspace(boundaries[0])
						require.Empty(t, issues)
						match, err = provenance.AffectedByFile(selectors, "/README.md", nil)
						require.NoError(t, err)
						require.False(t, match, "equal payload must not leak the old owner's capability")
					}
				}
			}
		})
	}
	t.Run("present nullable retains capability", func(t *testing.T) {
		value := dagql.NonNull(&WorkspaceSDK{Workspace: owners[1], Name: "optional"})
		result, err := dagql.NewResultForCall(value, moduleObjectTestSyntheticCall("optional-sdk", value))
		require.NoError(t, err)
		collector, err := newWorkspaceInputCollector(ctx)
		require.NoError(t, err)
		collector.retainedValue(result)
		selectors, issues := collector.result.forWorkspace(boundaries[1])
		require.Empty(t, issues)
		match, err := provenance.AffectedByFile(selectors, "/README.md", nil)
		require.NoError(t, err)
		require.True(t, match)
	})

}
