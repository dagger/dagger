package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type workspaceInputTestServer struct {
	*moduleObjectTestServer
	workspace *Workspace
}

func (s *workspaceInputTestServer) CurrentWorkspace(context.Context) (*Workspace, error) {
	return s.workspace, nil
}

func TestWorkspaceFindUpSelectors(t *testing.T) {
	for _, test := range []struct {
		name, cwd, from string
		included        []string
		excluded        []string
	}{
		{name: "root", cwd: ".", from: ".", included: []string{"/.env"}, excluded: []string{"/child/.env", "/.env/child"}},
		{name: "nested", cwd: "a/b", from: ".", included: []string{"/.env", "/a/.env", "/a/b/.env"}, excluded: []string{"/a/b/child/.env", "/other/.env", "/a/.env/child"}},
		{name: "relative from", cwd: "a/b", from: "..", included: []string{"/.env", "/a/.env"}, excluded: []string{"/a/b/.env"}},
		{name: "absolute from", cwd: "a/b", from: "/other", included: []string{"/.env", "/other/.env"}, excluded: []string{"/a/b/.env", "/other/child/.env"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selectors, err := workspaceFindUpSelectors(".env", test.cwd, test.from, "findUp")
			require.NoError(t, err)
			for _, candidate := range test.included {
				affected, err := provenance.AffectedByFile(selectors, candidate, nil)
				require.NoError(t, err)
				require.True(t, affected, candidate)
			}
			for _, candidate := range test.excluded {
				affected, err := provenance.AffectedByFile(selectors, candidate, nil)
				require.NoError(t, err)
				require.False(t, affected, candidate)
			}
		})
	}
	_, err := workspaceFindUpSelectors(".env", ".", "..", "findUp")
	require.Error(t, err)
}

func TestWorkspaceInputHostOperations(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	sourceServer := &workspaceInputTestServer{moduleObjectTestServer: server}
	root.Server = sourceServer
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "source-client", SessionID: "source-session"})
	srv.ObserveCalls(ObserveWorkspaceInputs)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Socket]{}))
	workspace := &Workspace{Address: "file:///workspace", ClientID: "source-client", Cwd: "."}
	sourceServer.workspace = workspace
	dagql.Fields[*Query]{
		dagql.Func("host", func(context.Context, *Query, struct{}) (*Host, error) { return &Host{}, nil }),
		dagql.NodeFunc("workspace", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Workspace], error) {
			return dagql.NewObjectResultForCurrentCall(ctx, srv, workspace)
		}),
	}.Install(srv)
	hostFindUpCalls := 0
	dagql.Fields[*Host]{
		dagql.Func("findUp", func(context.Context, *Host, struct{ Name string }) (dagql.Nullable[dagql.String], error) {
			hostFindUpCalls++
			return dagql.Null[dagql.String](), nil
		}),
		dagql.NodeFunc("directory", func(ctx context.Context, _ dagql.ObjectResult[*Host], _ struct{ Path string }) (dagql.ObjectResult[*Directory], error) {
			dir, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
			if err != nil {
				return dir, err
			}
			return dir.WithContentDigest(ctx, digest.FromString("equal directory contents"))
		}),
		dagql.NodeFunc("_sshAuthSocket", func(ctx context.Context, _ dagql.ObjectResult[*Host], _ struct{}) (dagql.Result[*Socket], error) {
			return dagql.NewResultForCurrentCall(ctx, &Socket{})
		}),
	}.Install(srv)
	dagql.Fields[*Workspace]{
		dagql.NodeFunc("snapshot", func(_ context.Context, parent dagql.ObjectResult[*Workspace], _ struct{}) (dagql.ObjectResult[*Workspace], error) {
			return parent, nil
		}).DoNotCache("Snapshot fixture returns its source"),
		dagql.NodeFunc("directory", func(ctx context.Context, _ dagql.ObjectResult[*Workspace], _ struct {
			Path    string
			Include []string
		}) (dir dagql.ObjectResult[*Directory], err error) {
			// Model the backend call made by a Workspace directory read. No
			// client filesystem is needed to test the observer's boundary.
			err = srv.Select(ctx, srv.Root(), &dir,
				dagql.Selector{Field: "host"},
				dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("/workspace")}}})
			return dir, err
		}),
	}.Install(srv)

	inspect := func(value dagql.AnyResult) workspaceInputSummary {
		t.Helper()
		id, err := value.ID()
		require.NoError(t, err)
		summary, err := workspaceInputsForCall(ctx, &dagql.ResultCall{Receiver: &dagql.ResultCallRef{ResultID: id.EngineResultID()}})
		require.NoError(t, err)
		return summary
	}

	// Capture the Host before the snapshot fixture uses it in a nested source operation.
	var host dagql.ObjectResult[*Host]
	require.NoError(t, srv.Select(ctx, srv.Root(), &host, dagql.Selector{Field: "host"}))
	var ws dagql.ObjectResult[*Workspace]
	require.NoError(t, srv.Select(ctx, srv.Root(), &ws, dagql.Selector{Field: "workspace"}))
	boundary, err := workspaceResultBoundary(ctx, ws)
	require.NoError(t, err)
	t.Run("native Workspace exact hit keeps its fixed source", func(t *testing.T) {
		var hit dagql.ObjectResult[*Workspace]
		require.NoError(t, srv.Select(ctx, srv.Root(), &hit, dagql.Selector{Field: "workspace"}))
		hitBoundary, err := workspaceResultBoundary(ctx, hit)
		require.NoError(t, err)
		require.Equal(t, boundary, hitBoundary)
		id, err := hit.ID()
		require.NoError(t, err)
		ref := &dagql.ResultCallRef{ResultID: id.EngineResultID()}
		previous, err := cache.ResultCallForRef(ref)
		require.NoError(t, err)
		stored, complete, err := cache.ResultFacts(ctx, "source-session", ref, workspaceInputsNamespace)
		require.NoError(t, err)
		require.True(t, complete)
		changed := previous.Clone()
		changed.Args = []*dagql.ResultCallArg{{Name: "different", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: "recipe"}}}
		observation := &workspaceInputCall{request: &dagql.CallRequest{ResultCall: changed, ReceiverTypeName: "Query"}, reads: &workspaceInputReads{}}
		rebound, _, err := observation.rebindFacts(ctx, previous, hit, stored, true)
		require.NoError(t, err)
		var fact workspaceInputFact
		require.NoError(t, json.Unmarshal(rebound[0].Payload, &fact))
		require.False(t, fact.CapabilitiesComplete, "a different recipe still needs an input binding")
		require.Empty(t, fact.Boundary)
	})
	t.Run("uncached snapshot keeps source boundary", func(t *testing.T) {
		var snapshot dagql.ObjectResult[*Workspace]
		require.NoError(t, srv.Select(ctx, ws, &snapshot, dagql.Selector{Field: "snapshot"}))
		snapshotBoundary, err := workspaceResultBoundary(ctx, snapshot)
		require.NoError(t, err)
		require.Equal(t, boundary, snapshotBoundary)
	})
	var directory dagql.ObjectResult[*Directory]
	require.NoError(t, srv.Select(ctx, ws, &directory,
		dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String("/")},
			{Name: "include", Value: dagql.ArrayInput[dagql.String]{"docs/**"}},
		}}))
	selectors, issues := inspect(directory).forWorkspace(boundary)
	require.Empty(t, issues)
	require.Equal(t, []provenance.Selector{{Path: "/", Include: []string{"docs/**"}, Exclude: []string{}, Kind: provenance.WorkspaceRead, Origin: "Workspace.directory"}}, selectors)

	var socket dagql.Result[*Socket]
	require.NoError(t, srv.Select(ctx, srv.Root(), &socket,
		dagql.Selector{Field: "host"}, dagql.Selector{Field: "_sshAuthSocket"}))
	selectors, issues = inspect(socket).forWorkspace(boundary)
	require.Empty(t, selectors)
	require.Empty(t, issues)

	var raw dagql.ObjectResult[*Directory]
	require.NoError(t, srv.Select(ctx, srv.Root(), &raw,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("/other")}}}))
	_, issues = inspect(raw).forWorkspace(boundary)
	require.Contains(t, issues, `Host.directory source input has no workspace source mapping (effective client "source-client")`)

	_, issues = inspect(host).forWorkspace(boundary)
	require.Contains(t, issues, "retained Host input has no workspace source mapping (receiver)")

	t.Run("null Host findUp survives discarded results and cache reuse", func(t *testing.T) {
		previous := sourceServer.workspace
		t.Cleanup(func() { sourceServer.workspace = previous })
		beforeCalls := hostFindUpCalls
		bodies := 0
		dagql.Fields[*Query]{
			dagql.NodeFunc("discardFindUp", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Directory], error) {
				bodies++
				var discarded dagql.Nullable[dagql.String]
				if err := srv.Select(ctx, srv.Root(), &discarded,
					dagql.Selector{Field: "host"},
					dagql.Selector{Field: "findUp", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(".env")}}}); err != nil {
					return dagql.ObjectResult[*Directory]{}, err
				}
				return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
			}),
		}.Install(srv)
		selected := workspace.Clone()
		selected.Cwd = "nested/leaf"
		sourceServer.workspace = selected
		firstBoundary, err := actualWorkspaceBoundary(ctx, selected)
		require.NoError(t, err)
		var first dagql.ObjectResult[*Directory]
		// An explicitly bound Workspace must not replace the Host caller's
		// selected Workspace or cwd.
		require.NoError(t, srv.Select(WorkspaceToContext(ctx, ws), srv.Root(), &first, dagql.Selector{Field: "discardFindUp"}))
		firstRules := inspect(first).FindUps
		require.Equal(t, []workspaceFindUpRule{{Name: ".env", Workspace: firstBoundary, Cwd: "nested/leaf"}}, firstRules)
		selected = selected.Clone()
		selected.Address = "file:///other-workspace"
		selected.Cwd = "other"
		sourceServer.workspace = selected
		// Read-only inspection keeps the producing caller's binding.
		require.Equal(t, firstRules, inspect(first).FindUps)
		var second dagql.ObjectResult[*Directory]
		require.NoError(t, srv.Select(ctx, srv.Root(), &second, dagql.Selector{Field: "discardFindUp"}))
		secondBoundary, err := actualWorkspaceBoundary(ctx, selected)
		require.NoError(t, err)
		require.Equal(t, []workspaceFindUpRule{{Name: ".env", Workspace: secondBoundary, Cwd: "other"}}, inspect(second).FindUps)
		require.Equal(t, 1, hostFindUpCalls-beforeCalls)
		require.Equal(t, 1, bodies)
	})

	t.Run("cached setup search reaches the next runtime snapshot", func(t *testing.T) {
		previous := sourceServer.workspace
		t.Cleanup(func() { sourceServer.workspace = previous })
		selected := workspace.Clone()
		selected.Cwd = "setup/cwd"
		sourceServer.workspace = selected
		setupBoundary, err := actualWorkspaceBoundary(ctx, selected)
		require.NoError(t, err)
		want := []workspaceFindUpRule{{Name: ".env", Workspace: setupBoundary, Cwd: "setup/cwd"}}
		recordSetup := func(ctx context.Context) {
			observation := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
			recordWorkspaceRuntimeInputs(ctx, nil, &FunctionCall{}, &moduleOwnershipRuntime{}, observation)
		}
		bodies := 0
		dagql.Fields[*Query]{
			dagql.NodeFunc("setupFindUp", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Directory], error) {
				bodies++
				var discarded dagql.Nullable[dagql.String]
				if err := srv.Select(ctx, srv.Root(), &discarded,
					dagql.Selector{Field: "host"},
					dagql.Selector{Field: "findUp", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(".env")}}}); err != nil {
					return dagql.ObjectResult[*Directory]{}, err
				}
				recordSetup(ctx)
				return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
			}),
			dagql.NodeFunc("discardSetupFindUp", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Directory], error) {
				var discarded dagql.ObjectResult[*Directory]
				if err := srv.Select(ctx, srv.Root(), &discarded, dagql.Selector{Field: "setupFindUp"}); err != nil {
					return dagql.ObjectResult[*Directory]{}, err
				}
				// The real setup boundary must receive cached child inputs
				// before it separates them from later body reads.
				recordSetup(ctx)
				return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
			}),
		}.Install(srv)
		var first dagql.ObjectResult[*Directory]
		require.NoError(t, srv.Select(ctx, srv.Root(), &first, dagql.Selector{Field: "setupFindUp"}))
		require.Equal(t, want, inspect(first).FindUps)
		selected = selected.Clone()
		selected.Address = "file:///next-workspace"
		selected.Cwd = "next/cwd"
		sourceServer.workspace = selected
		var hit, outer dagql.ObjectResult[*Directory]
		require.NoError(t, srv.Select(ctx, srv.Root(), &hit, dagql.Selector{Field: "setupFindUp"}))
		require.Equal(t, want, inspect(hit).FindUps, "setup inputs retain their producing caller")
		require.NoError(t, srv.Select(ctx, srv.Root(), &outer, dagql.Selector{Field: "discardSetupFindUp"}))
		require.Equal(t, want, inspect(outer).FindUps, "cached child completion supplies the next setup snapshot")
		require.Equal(t, 1, bodies)
		id, err := outer.ID()
		require.NoError(t, err)
		facts, _, err := cache.ResultFacts(ctx, "source-session", &dagql.ResultCallRef{ResultID: id.EngineResultID()}, workspaceInputsNamespace)
		require.NoError(t, err)
		var fact workspaceInputFact
		require.NoError(t, json.Unmarshal(facts[0].Payload, &fact))
		require.NotNil(t, fact.RuntimeInputs)
		require.Equal(t, want, fact.RuntimeInputs.FindUps)
		require.Empty(t, fact.FindUps, "setup searches are prepared inputs, not body events")
		prior, err := cache.ResultCallForRef(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
		require.NoError(t, err)
		current := prior.Clone()
		current.Args = []*dagql.ResultCallArg{{Name: "different", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: "recipe"}}}
		obs := &workspaceInputCall{request: &dagql.CallRequest{ResultCall: current, ReceiverTypeName: "Query"}, reads: &workspaceInputReads{}}
		next, ok, err := obs.rebindFacts(ctx, prior, outer, facts, true)
		require.NoError(t, err)
		require.True(t, ok)
		again, ok, err := obs.rebindFacts(ctx, current, outer, next, ok)
		require.NoError(t, err)
		require.True(t, ok)
		var after workspaceInputFact
		require.NoError(t, json.Unmarshal(again[0].Payload, &after))
		require.NotNil(t, after.RuntimeInputs, "structural then exact reuse lost the setup search")
		require.Equal(t, want, after.RuntimeInputs.FindUps)
	})

	t.Run("runtime Host reads retain prepared caller rules", func(t *testing.T) {
		lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "findUp test", nil, nil)
		defer lease.Release()
		scope, err := engine.NewClientScope(&engine.ClientMetadata{ClientID: "source-client", SessionID: "source-session"}, lease)
		require.NoError(t, err)
		scoped, err := engine.ContextWithClientScope(ctx, scope)
		require.NoError(t, err)
		rules := []workspaceFindUpRule{{Name: ".env", Workspace: "prepared-runtime-workspace", Cwd: "prepared/cwd"}}
		previous := server.mockServer.functionCall
		server.mockServer.functionCall = &FunctionCall{workspaceRuntimeInputs: &workspaceInputSummary{FindUps: rules}}
		t.Cleanup(func() { server.mockServer.functionCall = previous })
		req := &dagql.CallRequest{ReceiverTypeName: "Host", ResultCall: &dagql.ResultCall{Field: "findUp"}}
		observed, _, _ := ObserveWorkspaceInputs(scoped, req)
		observation := observed.Value(workspaceInputCallKey{}).(*workspaceInputCall)
		fact, err := observation.capture(observed, nil)
		require.NoError(t, err)
		require.Equal(t, rules, fact.Prepared.FindUps)
		require.Empty(t, fact.FindUps, "runtime inputs are prepared origins, not new body reads")
		legacy := fact
		legacy.Prepared.Issues = []string{"unresolved producing-client source"}
		legacy.Complete = false
		blob, err := json.Marshal(legacy)
		require.NoError(t, err)
		next, covered, err := observation.rebindFacts(observed, req.ResultCall, nil, []dagql.ResultFact{{Namespace: "workspace-inputs/v7", Payload: blob}}, true)
		require.NoError(t, err)
		require.Contains(t, string(next[len(next)-1].Payload), "unresolved producing-client source")
		require.False(t, covered, "legacy incomplete source on a prepared native operation became complete")
		reselected := &workspaceInputCall{request: req, reads: &workspaceInputReads{}, prepared: true}
		next, covered, err = reselected.rebindFacts(observed, req.ResultCall, nil, next, covered)
		require.NoError(t, err)
		require.False(t, covered)
		require.Contains(t, string(next[len(next)-1].Payload), "unresolved producing-client source")

		routed := engine.ContextWithClientMetadata(scoped, &engine.ClientMetadata{ClientID: "another-client", SessionID: "source-session"})
		_, err = bindWorkspaceFindUps(routed, rules)
		require.ErrorContains(t, err, "caller Workspace is unavailable")
	})

	t.Run("structural hit preserves overlapping body read", func(t *testing.T) {
		typ := NewObjectTypeDef("PreparedSourceProbe", "", nil)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{TypeDef: typ}}))
		bodies := 0
		prepareSources := dagql.NodeFunc("prepareSources", func(ctx context.Context, _ dagql.ObjectResult[*Query], args struct {
			Input     DirectoryID
			Workspace dagql.ID[*Workspace]
		}) (dagql.ObjectResult[*ModuleObject], error) {
			bodies++
			workspace, err := args.Workspace.Load(ctx, srv)
			if err != nil {
				return dagql.ObjectResult[*ModuleObject]{}, err
			}
			var discarded dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, workspace, &discarded, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "include", Value: dagql.ArrayInput[dagql.String]{"left/**"}},
			}}); err != nil {
				return dagql.ObjectResult[*ModuleObject]{}, err
			}
			input, err := args.Input.Load(ctx, srv)
			if err != nil {
				return dagql.ObjectResult[*ModuleObject]{}, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &ModuleObject{TypeDef: typ, Fields: map[string]any{"input": input}})
		})
		prepareSources.Spec.Type = &ModuleObject{TypeDef: typ}
		dagql.Fields[*Query]{prepareSources}.Install(srv)
		workspaceID, err := ws.ID()
		require.NoError(t, err)
		for _, region := range []string{"left/**", "right/**"} {
			var input dagql.ObjectResult[*Directory]
			require.NoError(t, srv.Select(ctx, ws, &input, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "include", Value: dagql.ArrayInput[dagql.String]{dagql.String(region)}},
			}}))
			inputID, err := input.ID()
			require.NoError(t, err)
			var prepared dagql.ObjectResult[*ModuleObject]
			require.NoError(t, srv.Select(ctx, srv.Root(), &prepared, dagql.Selector{Field: "prepareSources", Args: []dagql.NamedInput{
				{Name: "input", Value: dagql.NewID[*Directory](inputID)},
				{Name: "workspace", Value: dagql.NewID[*Workspace](workspaceID)},
			}}))
			selectors, issues := inspect(prepared).forWorkspace(boundary)
			require.Empty(t, issues)
			left, err := provenance.AffectedByFile(selectors, "/left/new.txt", nil)
			require.NoError(t, err)
			require.True(t, left, "the fixed body read must survive a changed prepared origin")
			if region == "right/**" {
				right, err := provenance.AffectedByFile(selectors, "/right/new.txt", nil)
				require.NoError(t, err)
				require.True(t, right, "the current prepared origin must replace the stored field origin")
			}
			unrelated, err := provenance.AffectedByFile(selectors, "/unrelated.txt", nil)
			require.NoError(t, err)
			require.False(t, unrelated)
		}
		require.Equal(t, 1, bodies, "equal inputs must reuse the prepared payload")
	})

	t.Run("current workspace keeps its source boundary across calls and routes", func(t *testing.T) {
		dagql.Fields[*Query]{
			dagql.NodeFunc("currentWorkspace", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Workspace], error) {
				result, err := dagql.NewObjectResultForCurrentCall(ctx, srv, sourceServer.workspace)
				if err != nil {
					return result, err
				}
				identity, ok, err := sourceServer.workspace.ContentIdentity(ctx)
				if err != nil || !ok {
					return result, err
				}
				return result.WithContentDigest(ctx, identity)
			}).WithInput(dagql.PerCallInput),
		}.Install(srv)
		value := &Workspace{Address: "directory://fixture", ClientID: "route-one", Cwd: "."}
		value.SetSource(NewWorkspaceSourceDirectory(directory))
		sourceServer.workspace = value
		var previous string
		for _, route := range []string{"route-one", "route-one", "route-two"} {
			current := value.Clone()
			current.ClientID = route
			sourceServer.workspace = current
			var result dagql.ObjectResult[*Workspace]
			require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "currentWorkspace"}))
			boundary, err := workspaceResultBoundary(ctx, result)
			require.NoError(t, err)
			if previous != "" {
				require.Equal(t, previous, boundary)
			}
			previous = boundary
		}
	})

	t.Run("ambiguous retained bindings stay unresolved through a view", func(t *testing.T) {
		fact := workspaceInputFact{Complete: true, CapabilitiesComplete: true, Boundary: "first", Retained: []workspaceRetainedInput{
			{Input: workspaceInputPath{"argument:first"}, Workspace: "first"},
			{Input: workspaceInputPath{"argument:second"}, Workspace: "second"},
		}}
		validateWorkspaceRetainedBindings(&fact)
		require.Len(t, fact.Retained, 2)
		require.False(t, fact.CapabilitiesComplete)
		require.Empty(t, fact.Boundary)
		ambiguous, err := cache.GetOrInitCall(ctx, "source-session", srv, &dagql.CallRequest{
			ResultCall:  &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "ambiguous_workspace", Type: dagql.NewResultCallType(workspace.Type())},
			ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true,
		}, func(context.Context) (dagql.AnyResult, error) { return ws, nil })
		require.NoError(t, err)
		dagql.Fields[*Workspace]{
			dagql.NodeFunc("withWorkdir", func(ctx context.Context, parent dagql.ObjectResult[*Workspace], args struct{ Path string }) (dagql.ObjectResult[*Workspace], error) {
				value := parent.Self().Clone()
				value.Cwd = args.Path
				return dagql.NewObjectResultForCurrentCall(ctx, srv, value)
			}),
		}.Install(srv)
		var view dagql.ObjectResult[*Workspace]
		object, err := srv.ToSelectable(ctx, ambiguous)
		require.NoError(t, err)
		require.NoError(t, srv.Select(ctx, object, &view, dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("docs")}}}))
		_, err = workspaceResultBoundary(ctx, view)
		require.ErrorContains(t, err, "no complete source boundary")
	})

	t.Run("loaded_source_origin", func(t *testing.T) {
		var source dagql.ObjectResult[*Directory]
		require.NoError(t, srv.Select(ctx, ws, &source, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String("/")},
			{Name: "include", Value: dagql.ArrayInput[dagql.String]{"loaded/**"}},
		}}))
		sourceID, err := source.ID()
		require.NoError(t, err)
		typ := NewObjectTypeDef("LoadedSourceProbe", "", nil)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{TypeDef: typ}}))
		var body workspaceInputSummary
		retainLoadedSource := dagql.NodeFunc("retainLoadedSource", func(ctx context.Context, _ dagql.ObjectResult[*Query], args struct{ Source DirectoryID }) (dagql.ObjectResult[*Directory], error) {
			return args.Source.Load(ctx, srv)
		})
		prepareLoadedSource := dagql.NodeFunc("prepareLoadedSource", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*ModuleObject], error) {
			var input dagql.ObjectResult[*Directory]
			if err := srv.Select(ctx, srv.Root(), &input, dagql.Selector{Field: "retainLoadedSource", Args: []dagql.NamedInput{{Name: "source", Value: dagql.NewID[*Directory](sourceID)}}}); err != nil {
				return dagql.ObjectResult[*ModuleObject]{}, err
			}
			body = currentWorkspaceInputReads(ctx).snapshot()
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &ModuleObject{TypeDef: typ, Fields: map[string]any{"input": input}})
		})
		prepareLoadedSource.Spec.Type = &ModuleObject{TypeDef: typ}
		dagql.Fields[*Query]{retainLoadedSource, prepareLoadedSource}.Install(srv)
		var prepared dagql.ObjectResult[*ModuleObject]
		require.NoError(t, srv.Select(ctx, srv.Root(), &prepared, dagql.Selector{Field: "prepareLoadedSource"}))
		before, beforeIssues := body.forWorkspace(boundary)
		// The nested call imports an external ID, outside this parent's
		// prepared inputs. Completion checks it before the body snapshot.
		require.Equal(t, []string{`body input has a source origin not bound to prepared inputs or recorded source operations: ["","","","Workspace","directory"]`}, beforeIssues)
		require.Empty(t, body.reads, "loading an existing origin does not issue a new Workspace query")
		beforeMatch, err := provenance.AffectedByFile(before, "/loaded/input.txt", nil)
		require.NoError(t, err)
		require.True(t, beforeMatch, "the completed child call supplies the loaded input origin")
		after, afterIssues := inspect(prepared).forWorkspace(boundary)
		t.Logf("body selectors=%+v; body query events=%d; captured selectors=%+v; issues=%v", before, len(body.reads), after, afterIssues)
		require.Contains(t, strings.Join(afterIssues, "\n"), "body input has a source origin not bound to prepared inputs or recorded source operations:")
		afterMatch, err := provenance.AffectedByFile(after, "/loaded/input.txt", nil)
		require.NoError(t, err)
		require.True(t, afterMatch, "the returned object's loaded input origin must survive capture")
	})
	t.Run("cached consumer uses its current input's source", func(t *testing.T) {
		previous := sourceServer.workspace
		t.Cleanup(func() { sourceServer.workspace = previous })
		dagql.Fields[*Query]{dagql.NodeFunc("sourceForConsumer", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Directory], error) {
			var path dagql.Nullable[dagql.String]
			if err := srv.Select(ctx, host, &path, dagql.Selector{Field: "findUp", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(".env")}}}); err != nil {
				return dagql.ObjectResult[*Directory]{}, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
		})}.Install(srv)
		dagql.Fields[*Directory]{dagql.NodeFunc("consumeFixture", func(ctx context.Context, _ dagql.ObjectResult[*Directory], _ struct{}) (dagql.ObjectResult[*Directory], error) {
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
		})}.Install(srv)
		for _, cwd := range []string{"a", "b"} {
			selected := workspace.Clone()
			selected.Cwd = cwd
			sourceServer.workspace = selected
			var result dagql.ObjectResult[*Directory]
			require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "sourceForConsumer"}, dagql.Selector{Field: "consumeFixture"}))
			require.Equal(t, cwd == "b", result.HitCache())
			boundary, err := actualWorkspaceBoundary(ctx, selected)
			require.NoError(t, err)
			selectors, issues := inspect(result).forWorkspace(boundary)
			require.Empty(t, issues)
			provenance := newWorkspaceProvenance(selectors)
			for _, path := range []string{"a/.env", "b/.env", "README.md"} {
				match, err := provenance.AffectedByChangedPaths(&ChangesetPaths{Modified: []string{path}}, nil, nil)
				require.NoError(t, err)
				require.Equal(t, path == cwd+"/.env", match, path)
			}
		}
	})
}
