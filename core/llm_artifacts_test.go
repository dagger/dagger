package core

import (
	"context"
	"slices"
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

		// An address that selects nothing is refused, rather than handing
		// the function an empty selection it would vacuously accept.
		_, err = call(t, m, "check", map[string]any{"targets": "go/lnt"})
		require.ErrorContains(t, err, `"go/lnt" is not a resolvable Artifacts address: no artifact matches dag://go/lnt`)
		require.ErrorContains(t, err, "FindArtifacts lists what exists")
	})

	t.Run("an Artifact arg takes the one artifact", func(t *testing.T) {
		out, err := call(t, m, "eval", map[string]any{"target": "dag://go/test"})
		require.NoError(t, err)
		require.Equal(t, "go/test via conversation.artifacts.filterUri.one", out)

		// Several matches are an error listing them, pointing to discovery.
		_, err = call(t, m, "eval", map[string]any{"target": "go/*"})
		require.ErrorContains(t, err, `"go/*" is not a resolvable Artifact address`)
		require.ErrorContains(t, err, "dag://go/* matches 2 artifacts:\ndag://?check=go/lint\ndag://?check=go/test")
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

// TestQualifyCollidingTrees covers bound objects of one module whose trees
// share a path: each colliding tree is qualified by its bound type, so every
// artifact has an address of its own, and the plain address still selects
// them all.
func TestQualifyCollidingTrees(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "qualify-test",
		SessionID: "qualify-test",
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*scopeLiftRunner]{Typed: &scopeLiftRunner{}}))
	dagql.Fields[*Query]{
		dagql.Func("runner", func(context.Context, *Query, struct{}) (*scopeLiftRunner, error) {
			return &scopeLiftRunner{}, nil
		}),
	}.Install(srv)
	var value dagql.AnyObjectResult
	require.NoError(t, srv.Select(ctx, srv.Root(), &value, dagql.Selector{Field: "runner"}))

	// tree builds a bound object's artifacts, as BoundArtifacts roots them:
	// under the module name, below a synthetic parent.
	tree := func(typeName string, fields ...string) boundTree {
		root := &ModTreeNode{Name: "staff", RootValue: value, Parent: &ModTreeNode{}}
		var entries []*Artifact
		for _, field := range fields {
			parent := root
			for segment := range strings.SplitSeq(field, "/") {
				node := &ModTreeNode{Name: segment, Parent: parent}
				entries = append(entries, &Artifact{ModuleName: "staff", Path: node.CLIPath(), TypeName: "Directory", Node: node})
				parent = node
			}
		}
		return boundTree{typeName: typeName, entries: entries}
	}
	view := tree("StaffView", "members/head")
	pulls := tree("StaffPullTools", "members/head", "logOf")
	line := tree("StaffChiefLine", "inbox")
	qualifyCollidingTrees([]boundTree{view, pulls, line})

	var all []*Artifact
	var paths []string
	for _, tree := range []boundTree{view, pulls, line} {
		for _, entry := range tree.entries {
			all = append(all, entry)
			paths = append(paths, strings.Join(entry.Path, "/"))
		}
	}
	require.Equal(t, []string{
		"staff/staff-view/members",
		"staff/staff-view/members/head",
		"staff/staff-pull-tools/members",
		"staff/staff-pull-tools/members/head",
		// The colliding tree is qualified as a whole.
		"staff/staff-pull-tools/log-of",
		// A tree that collides with no other keeps its plain paths.
		"staff/inbox",
	}, paths)
	// The root keeps its value: evaluation is unchanged.
	require.NotNil(t, view.entries[0].BoundRoot())

	scope := &Artifacts{Entries: all}
	filter := func(addr string) *Artifacts {
		t.Helper()
		parsed, err := dagaddress.Parse(addr)
		require.NoError(t, err)
		selected, err := scope.FilterURI(parsed)
		require.NoError(t, err)
		return selected
	}
	// A qualified address names one artifact.
	one, err := filter("dag://staff/staff-pull-tools/members/head").One()
	require.NoError(t, err)
	require.Equal(t, []string{"staff", "staff-pull-tools", "members", "head"}, one.Path)
	// The plain address selects the artifact of every colliding object; where
	// one is required, the error lists addresses that tell them apart.
	_, err = filter("dag://staff/members/head").One()
	require.ErrorContains(t, err, "matches 2 artifacts:\ndag://?directory=staff/staff-view/members/head\ndag://?directory=staff/staff-pull-tools/members/head")
	require.Len(t, filter("staff/members/*").Entries, 2)
	require.Len(t, filter("staff/inbox").Entries, 1)
	// Include patterns match the plain path too.
	match, err := matchesInclude(pulls.entries[2], IncludePatterns("staff/log-of"))
	require.NoError(t, err)
	require.True(t, match)
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
	paths := func(selection *Artifacts) []string {
		var paths []string
		for _, entry := range selection.Entries {
			paths = append(paths, strings.Join(entry.Path, "/"))
		}
		return paths
	}

	all, err := mergeScopeArtifacts(bound, shadowed, nil, &Artifacts{Entries: workspace}, nil)
	require.NoError(t, err)
	require.Nil(t, all.Selector.Paths)
	require.Equal(t, []string{"app/build", "git-tools/head", "roster/members", "roster/members/dir"}, paths(all))
	require.Same(t, bound[0], all.Entries[2], "the bound roster wins")

	// Workspace.artifacts narrows its entries but keeps all loaded dimensions.
	narrowedWorkspace := &Artifacts{Entries: workspace[3:], AllDimensions: (&Artifacts{Entries: workspace}).DimensionDefinitions()}
	narrowed, err := mergeScopeArtifacts(bound, shadowed, nil, narrowedWorkspace, []string{"roster:members"})
	require.NoError(t, err)
	require.Equal(t, []string{"roster/members/**"}, narrowed.Selector.Paths)
	// The workspace part arrives already narrowed by Workspace.artifacts;
	// include narrows the bound part the same way.
	require.Equal(t, []string{"app/build", "roster/members", "roster/members/dir"}, paths(narrowed))
	// include skips git-tools/head, but its dimensions still count for names.
	require.True(t, slices.ContainsFunc(narrowed.AllDimensions, func(d *ArtifactDimension) bool { return d.Identifier == "type:GitRef" }))
	require.Equal(t, "container", narrowed.Entries[0].DimensionNames["type:Container"])

	// A roster bound as a fresh construction has the workspace's values: the
	// workspace's roster wins, entrypoint shorthand and all.
	fresh := map[string]bool{"roster": true}
	all, err = mergeScopeArtifacts(bound, shadowed, fresh, &Artifacts{Entries: workspace}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"app/build", "git-tools/head", "lint", "roster/load", "roster/members"}, paths(all))
	require.Same(t, workspace[0], all.Entries[4], "the workspace roster wins")
	// Not where the workspace has no roster, nor one that failed to load:
	// the binding is all there is.
	for _, ws := range [][]*Artifact{workspace[3:], workspace[1:2]} {
		all, err = mergeScopeArtifacts(bound, shadowed, fresh, &Artifacts{Entries: ws}, nil)
		require.NoError(t, err)
		require.Contains(t, paths(all), "roster/members/dir")
		require.NotContains(t, paths(all), "roster/load")
	}
}
