package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// scopeLiftRunner is a tool object whose methods take artifact selections.
type scopeLiftRunner struct{}

func (*scopeLiftRunner) Type() *ast.Type {
	return &ast.Type{NamedType: "ScopeLiftRunner", NonNull: true}
}

// TestLiftScopeSelection covers lifting an address into an Artifacts or
// Artifact argument: the selection is selected through the dispatching
// conversation — artifacts(include: [<path>]).filterUri(uri: <address>), plus
// one for an Artifact — so the module function receives a loadable ID with
// that recipe. LLM.artifacts and the filters are stubbed with their core
// implementations over a fixed scope.
func TestLiftScopeSelection(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "scope-lift-test",
		SessionID: "scope-lift-test",
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*LLM]{Typed: &LLM{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Artifacts]{Typed: &Artifacts{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Artifact]{Typed: &Artifact{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*scopeLiftRunner]{Typed: &scopeLiftRunner{}}))

	scope := []*Artifact{
		{ModuleName: "go", Path: []string{"go", "lint"}, TypeName: "Check"},
		{ModuleName: "go", Path: []string{"go", "test"}, TypeName: "Check"},
		{ModuleName: "docs", Path: []string{"docs", "site"}, TypeName: "Directory"},
	}
	var includes [][]string
	dagql.Fields[*Query]{
		dagql.Func("conversation", func(context.Context, *Query, struct{}) (*LLM, error) {
			return &LLM{mcp: newMCP()}, nil
		}),
		dagql.Func("runner", func(context.Context, *Query, struct{}) (*scopeLiftRunner, error) {
			return &scopeLiftRunner{}, nil
		}),
	}.Install(srv)
	dagql.Fields[*LLM]{
		dagql.Func("artifacts", func(_ context.Context, _ *LLM, args struct {
			Include dagql.Optional[dagql.ArrayInput[dagql.String]]
		}) (*Artifacts, error) {
			var include []string
			for _, pattern := range args.Include.Value {
				include = append(include, pattern.String())
			}
			includes = append(includes, include)
			return &Artifacts{Entries: scope}, nil
		}),
		// The real withTools rebinds; this one only marks the fold.
		dagql.Func("withTools", func(_ context.Context, llm *LLM, _ struct {
			Object  dagql.AnyID
			Except  []string                     `default:"[]"`
			Version int                          `default:"0"`
			Owner   dagql.Optional[dagql.String] `internal:"true"`
		}) (*LLM, error) {
			return llm, nil
		}),
	}.Install(srv)
	dagql.Fields[*Artifacts]{
		dagql.Func("filterUri", func(_ context.Context, a *Artifacts, args struct{ URI string }) (*Artifacts, error) {
			addr, err := dagaddress.Parse(args.URI)
			if err != nil {
				return nil, err
			}
			return a.FilterURI(addr)
		}),
		dagql.Func("one", func(_ context.Context, a *Artifacts, _ struct{}) (*Artifact, error) {
			return a.One()
		}),
	}.Install(srv)
	// recipe renders the chain of calls that produced a lifted argument.
	recipe := func(ctx context.Context, res dagql.AnyObjectResult) (string, error) {
		id, err := res.RecipeID(ctx)
		if err != nil {
			return "", err
		}
		var fields []string
		for ; id != nil; id = id.Receiver() {
			fields = append([]string{id.Field()}, fields...)
		}
		return strings.Join(fields, "."), nil
	}
	dagql.Fields[*scopeLiftRunner]{
		dagql.Func("check", func(ctx context.Context, _ *scopeLiftRunner, args struct {
			Targets dagql.ID[*Artifacts]
		}) (dagql.String, error) {
			targets, err := args.Targets.Load(ctx, srv)
			if err != nil {
				return "", err
			}
			via, err := recipe(ctx, targets)
			if err != nil {
				return "", err
			}
			var paths []string
			for _, entry := range targets.Self().Entries {
				paths = append(paths, strings.Join(entry.Path, "/"))
			}
			return dagql.String(strings.Join(paths, ",") + " via " + via), nil
		}),
		dagql.Func("eval", func(ctx context.Context, _ *scopeLiftRunner, args struct {
			Target dagql.ID[*Artifact]
		}) (dagql.String, error) {
			target, err := args.Target.Load(ctx, srv)
			if err != nil {
				return "", err
			}
			via, err := recipe(ctx, target)
			if err != nil {
				return "", err
			}
			return dagql.String(strings.Join(target.Self().Path, "/") + " via " + via), nil
		}),
	}.Install(srv)

	var conversation dagql.ObjectResult[*LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &conversation, dagql.Selector{Field: "conversation"}))
	var runner dagql.AnyObjectResult
	require.NoError(t, srv.Select(ctx, srv.Root(), &runner, dagql.Selector{Field: "runner"}))
	runnerType := srv.Schema().Types["ScopeLiftRunner"]
	// The dispatching conversation and this step's MCP agree: nothing to fold.
	m := newMCP()
	m.SetSelfLLM(conversation)
	call := func(t *testing.T, m *MCP, method string, args map[string]any) (string, error) {
		t.Helper()
		sel, err := m.buildObjectMethodSelector(ctx, srv, runner.ObjectType(), fieldByName(runnerType, method), args)
		if err != nil {
			return "", err
		}
		var out dagql.String
		err = srv.Select(ctx, runner, &out, sel)
		return out.String(), err
	}

	t.Run("an Artifacts arg takes the selection", func(t *testing.T) {
		includes = nil
		out, err := call(t, m, "check", map[string]any{"targets": "go/**"})
		require.NoError(t, err)
		require.Equal(t, "go/lint,go/test via conversation.artifacts.filterUri", out)
		// The path narrows which workspace modules load.
		require.Equal(t, [][]string{{"go/**"}}, includes)

		out, err = call(t, m, "check", map[string]any{"targets": "dag+directory://"})
		require.NoError(t, err)
		require.Equal(t, "docs/site via conversation.artifacts.filterUri", out)
		require.Nil(t, includes[len(includes)-1], "an address without a path loads everything")
	})

	t.Run("an Artifact arg takes the one artifact", func(t *testing.T) {
		out, err := call(t, m, "eval", map[string]any{"target": "dag://go/test"})
		require.NoError(t, err)
		require.Equal(t, "go/test via conversation.artifacts.filterUri.one", out)

		// Several matches are an error listing them, pointing to discovery.
		_, err = call(t, m, "eval", map[string]any{"target": "go/*"})
		require.ErrorContains(t, err, `"go/*" is not a resolvable Artifact address`)
		require.ErrorContains(t, err, "matches 2 artifacts:\ndag://go/lint\ndag://go/test")
		require.ErrorContains(t, err, "FindArtifacts lists what exists")
	})

	t.Run("this turn's tool state is folded in", func(t *testing.T) {
		// An earlier call of the turn rebound a tool: the scope is that of
		// the conversation with the rebinding recorded, as step() records
		// it.
		rebound := m.WithTools(runner, srv.Schema(), nil)
		rebound.SetSelfLLM(conversation)
		out, err := call(t, rebound, "check", map[string]any{"targets": "docs/**"})
		require.NoError(t, err)
		require.Equal(t, "docs/site via conversation.withTools.artifacts.filterUri", out)
	})

	t.Run("an address needs a conversation", func(t *testing.T) {
		_, err := call(t, newMCP(), "check", map[string]any{"targets": "go/**"})
		require.ErrorContains(t, err, "no conversation to resolve the address in")
	})
}

func TestSelectScopeBindings(t *testing.T) {
	modules, selected := selectScopeBindings([]scopeBinding{
		{typeName: "StaffPullTools", moduleName: "staff"},
		{typeName: "Committer", moduleName: "committer", main: true},
		{typeName: "Staff", moduleName: "staff", main: true},
		{typeName: "GitToolsView", moduleName: "git-tools"},
		{typeName: "GitToolsAdmin", moduleName: "git-tools"},
	})
	require.Equal(t, []string{"staff", "committer", "git-tools"}, modules)
	typeNames := func(module string) []string {
		var names []string
		for _, b := range selected[module] {
			names = append(names, b.typeName)
		}
		return names
	}
	// A bound main object is the module's whole address space.
	require.Equal(t, []string{"Staff"}, typeNames("staff"))
	require.Equal(t, []string{"Committer"}, typeNames("committer"))
	// Without it, every bound object of the module contributes its tree.
	require.Equal(t, []string{"GitToolsView", "GitToolsAdmin"}, typeNames("git-tools"))
}

func TestMergeScopeArtifacts(t *testing.T) {
	bound := []*Artifact{
		{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory"},
		{ModuleName: "roster", Path: []string{"roster", "members", "dir"}, TypeName: "Directory"},
		{ModuleName: "git-tools", Path: []string{"git-tools", "head"}, TypeName: "GitRef"},
	}
	workspace := []*Artifact{
		// The workspace's own roster is shadowed by the bound one, including
		// its load failure and its entrypoint shorthand.
		{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory"},
		{ModuleName: "roster", Path: []string{"roster", "load"}, TypeName: "Check", LoadFailure: &ModuleLoadFailure{Name: "roster"}},
		{ModuleName: "Roster", Path: []string{"lint"}, TypeName: "Check"},
		{ModuleName: "app", Path: []string{"app", "build"}, TypeName: "Container"},
	}
	shadowed := map[string]bool{"roster": true, "git-tools": true}
	uris := func(selection *Artifacts) []string {
		var uris []string
		for _, entry := range selection.Entries {
			uri, err := entry.URI(ArtifactURIOpts{})
			require.NoError(t, err)
			uris = append(uris, uri)
		}
		return uris
	}

	all, err := mergeScopeArtifacts(bound, shadowed, workspace, nil)
	require.NoError(t, err)
	require.Nil(t, all.Selector.Paths)
	require.Equal(t, []string{"dag://app/build", "dag://git-tools/head", "dag://roster/members", "dag://roster/members/dir"}, uris(all))
	require.Same(t, bound[0], all.Entries[2], "the bound roster wins")

	narrowed, err := mergeScopeArtifacts(bound, shadowed, workspace[3:], []string{"roster:members"})
	require.NoError(t, err)
	require.Equal(t, []string{"roster/members/**"}, narrowed.Selector.Paths)
	// The workspace part arrives already narrowed by Workspace.artifacts;
	// include narrows the bound part the same way.
	require.Equal(t, []string{"dag://app/build", "dag://roster/members", "dag://roster/members/dir"}, uris(narrowed))
}
