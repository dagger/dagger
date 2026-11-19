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

type workspaceContextInputTestServer struct {
	*moduleObjectTestServer
	current dagql.ObjectResult[*Module]
}

func (s *workspaceContextInputTestServer) CurrentModule(context.Context) (dagql.ObjectResult[*Module], error) {
	return s.current, nil
}

// This fixture consumes the ambient module through the real execMeta branch.
// Its tests also apply to the prior implementation, without helper shims.
func TestWorkspaceInputNativeContext(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &workspaceContextInputTestServer{moduleObjectTestServer: &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{
		ClientID: "context-client", SessionID: "context-session",
	})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Module]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	dagql.Fields[*Module]{dagql.NodeFunc("_implementationScoped", func(callCtx context.Context, self dagql.ObjectResult[*Module], _ struct{}) (dagql.ObjectResult[*Module], error) {
		return self.WithContentDigest(callCtx, digest.FromString("context-module-"+self.Self().NameField))
	})}.Install(srv)

	const boundary = "context-workspace"
	attachInput := func(name, sourcePath, cwd string, value dagql.AnyResult, incomplete bool) dagql.AnyResult {
		t.Helper()
		frame := moduleObjectTestSyntheticCall(name, value)
		fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: !incomplete, CapabilitiesComplete: true,
			Prepared: workspaceInputSummary{Sources: []workspaceInputSource{{Workspace: boundary, KnownBoundary: true,
				Selectors: []provenance.Selector{{Path: sourcePath, Kind: provenance.WorkspaceRead, Origin: name}}}}}}
		if cwd != "" {
			fact.Prepared.FindUps = []workspaceFindUpRule{{Workspace: boundary, Cwd: cwd, Name: ".env"}}
		}
		if incomplete {
			fact.Prepared.Issues = []string{"fixture context source is incomplete"}
		}
		result, err := cache.GetOrInitCall(ctx, "context-session", srv, &dagql.CallRequest{
			ResultCall: frame, ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true, ResultFactsIncomplete: incomplete,
		}, func(context.Context) (dagql.AnyResult, error) { return value, nil })
		require.NoError(t, err)
		return result
	}
	moduleInput := func(name string, incomplete bool) dagql.ObjectResult[*Module] {
		t.Helper()
		mod := &Module{NameField: name}
		value, err := dagql.NewObjectResultForCall(mod, srv, moduleObjectTestSyntheticCall("value-"+name, mod))
		require.NoError(t, err)
		return attachInput("input-"+name, "/native-"+name, "owned/"+name, value, incomplete).(dagql.ObjectResult[*Module])
	}
	a, b := moduleInput("a", false), moduleInput("b", false)
	incomplete := moduleInput("incomplete", true)
	directoryInput := func(name string) dagql.AnyResult {
		t.Helper()
		value, err := dagql.NewObjectResultForCall(&Directory{}, srv, moduleObjectTestSyntheticCall("directory-"+name, &Directory{}))
		require.NoError(t, err)
		attached := attachInput("arg-"+name, "/arg-"+name, "", value, false)
		attached, err = attached.WithContentDigestAny(ctx, digest.FromString("same-context-argument"))
		require.NoError(t, err)
		return attached
	}
	argA, argB := directoryInput("a"), directoryInput("b")
	argADigest, err := argA.(dagql.ObjectResult[*Directory]).ContentPreferredDigest(ctx)
	require.NoError(t, err)
	argBDigest, err := argB.(dagql.ObjectResult[*Directory]).ContentPreferredDigest(ctx)
	require.NoError(t, err)
	require.Equal(t, digest.FromString("same-context-argument"), argADigest)
	require.Equal(t, argADigest, argBDigest, "published argument aliases must have equal content identity")

	type contextArgs struct {
		Input DirectoryID
		Mode  string
	}
	consumeRuns := 0
	dagql.Fields[*Query]{
		dagql.NodeFunc("consumeContext", func(callCtx context.Context, _ dagql.ObjectResult[*Query], args contextArgs) (dagql.Result[dagql.String], error) {
			consumeRuns++
			_, err := (&Container{}).execMeta(callCtx, ContainerExecOpts{}, nil, dagql.ObjectResult[*Module]{})
			if err != nil {
				return dagql.Result[dagql.String]{}, err
			}
			if args.Mode == "unrelated" {
				if _, err := ImplementationScopedModule(callCtx, b); err != nil {
					return dagql.Result[dagql.String]{}, err
				}
			}
			return dagql.NewResultForCurrentCall(callCtx, dagql.String("consumed"))
		}),
		dagql.NodeFunc("discardContext", func(callCtx context.Context, _ dagql.ObjectResult[*Query], args contextArgs) (dagql.Result[dagql.String], error) {
			var discarded dagql.String
			if err := srv.Select(callCtx, srv.Root(), &discarded, dagql.Selector{Field: "consumeContext", Args: []dagql.NamedInput{
				{Name: "input", Value: args.Input}, {Name: "mode", Value: dagql.String(args.Mode)},
			}}); err != nil {
				return dagql.Result[dagql.String]{}, err
			}
			return dagql.NewResultForCurrentCall(callCtx, dagql.String("discarded"))
		}),
	}.Install(srv)
	srv.ObserveCalls(ObserveWorkspaceInputs)
	selectDiscard := func(arg dagql.AnyResult, mode string) (dagql.AnyResult, workspaceInputSummary) {
		t.Helper()
		id, err := arg.ID()
		require.NoError(t, err)
		ref := &dagql.ResultCallRef{ResultID: id.EngineResultID()}
		prepared, err := workspaceInputsForCall(ctx, &dagql.ResultCall{Receiver: ref})
		require.NoError(t, err)
		parent := &workspaceInputReads{summary: prepared}
		parent.rememberInputs(map[uint64]*dagql.ResultCallRef{ref.ResultID: ref})
		callCtx := context.WithValue(ctx, workspaceInputReadsKey{}, parent)
		result, err := srv.Root().Select(callCtx, srv, dagql.Selector{Field: "discardContext", Args: []dagql.NamedInput{
			{Name: "input", Value: dagql.NewID[*Directory](id)}, {Name: "mode", Value: dagql.String(mode)},
		}})
		require.NoError(t, err)
		return result, parent.snapshot()
	}
	inspect := func(value dagql.AnyResult) workspaceInputSummary {
		t.Helper()
		id, err := value.ID()
		require.NoError(t, err)
		summary, err := workspaceInputsForCall(ctx, &dagql.ResultCall{Receiver: &dagql.ResultCallRef{ResultID: id.EngineResultID()}})
		require.NoError(t, err)
		return summary
	}
	assertSources := func(summary workspaceInputSummary, argument string) {
		t.Helper()
		selectors, issues := summary.forWorkspace(boundary)
		require.Empty(t, issues)
		require.False(t, summary.incomplete)
		for _, path := range []string{"/native-a", "/owned/a/.env", "/arg-" + argument} {
			affected, err := provenance.AffectedByFile(selectors, path, nil)
			require.NoError(t, err)
			require.True(t, affected, path)
		}
		for _, path := range []string{"/native-b", "/owned/b/.env", "/README.md"} {
			affected, err := provenance.AffectedByFile(selectors, path, nil)
			require.NoError(t, err)
			require.False(t, affected, path)
		}
		other := "a"
		if argument == "a" {
			other = "b"
		}
		affected, err := provenance.AffectedByFile(selectors, "/arg-"+other, nil)
		require.NoError(t, err)
		require.False(t, affected, "structural reuse must use the current argument source")
	}

	server.current = a
	first, live := selectDiscard(argA, "normal")
	assertSources(live, "a")
	assertSources(inspect(first), "a")
	require.Equal(t, 1, consumeRuns)
	firstID, err := first.ID()
	require.NoError(t, err)
	beforeFacts, beforeComplete, err := cache.ResultFacts(ctx, "context-session", &dagql.ResultCallRef{ResultID: firstID.EngineResultID()}, workspaceInputsNamespace)
	require.NoError(t, err)
	beforeJSON, err := json.Marshal(beforeFacts)
	require.NoError(t, err)

	// The cached value was produced with module a. Changing ambient module
	// metadata cannot make this value depend on module b without execution.
	server.current = b
	hit, live := selectDiscard(argA, "normal")
	require.True(t, hit.HitCache())
	assertSources(live, "a")
	assertSources(inspect(hit), "a")
	require.Equal(t, 1, consumeRuns)

	reused, live := selectDiscard(argB, "normal")
	require.True(t, reused.HitCache(), "equal argument content must reuse the producer")
	assertSources(live, "b")
	assertSources(inspect(reused), "b")
	require.Equal(t, 1, consumeRuns)
	afterFacts, afterComplete, err := cache.ResultFacts(ctx, "context-session", &dagql.ResultCallRef{ResultID: firstID.EngineResultID()}, workspaceInputsNamespace)
	require.NoError(t, err)
	afterJSON, err := json.Marshal(afterFacts)
	require.NoError(t, err)
	require.Equal(t, beforeComplete, afterComplete)
	require.Equal(t, string(beforeJSON), string(afterJSON), "reuse must not overwrite the old producer's source records")

	server.current = incomplete
	bad, live := selectDiscard(argA, "incomplete")
	for _, summary := range []workspaceInputSummary{live, inspect(bad)} {
		_, issues := summary.forWorkspace(boundary)
		require.True(t, summary.incomplete)
		require.Contains(t, issues, "fixture context source is incomplete")
	}

	server.current = a
	negative, live := selectDiscard(argA, "unrelated")
	for _, summary := range []workspaceInputSummary{live, inspect(negative)} {
		_, issues := summary.forWorkspace(boundary)
		require.True(t, summary.incomplete)
		require.Contains(t, issues, "body input has a source origin not bound to prepared inputs or recorded source operations: "+workspaceInputOperation(ctx, moduleObjectTestSyntheticCall("input-b", b)))
	}
}
