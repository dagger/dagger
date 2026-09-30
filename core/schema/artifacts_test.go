package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestArtifactDirectiveFilterDoesNotRequireWorkspace(t *testing.T) {
	// Raw metadata filters must work without workspace settings or valid
	// command result types. Command filters enforce those rules separately.
	check := &core.Artifact{Path: []string{"check"}, TypeName: "Container", Directives: []string{"check"}}
	other := &core.Artifact{Path: []string{"other"}, TypeName: "Check"}
	all := &core.Artifacts{Entries: []*core.Artifact{check, other}}
	schema := &artifactsSchema{}
	included, err := schema.filterDirectives(t.Context(), all, artifactDirectiveFilterArgs{Directives: []string{"check"}})
	require.NoError(t, err)
	require.Equal(t, []*core.Artifact{check}, included.Entries)
	excluded, err := schema.filterDirectives(t.Context(), all, artifactDirectiveFilterArgs{Directives: []string{"check"}, Exclude: true})
	require.NoError(t, err)
	require.Equal(t, []*core.Artifact{other}, excluded.Entries)
}

func TestArtifactAbsoluteURIOnEmptySelection(t *testing.T) {
	schema := &artifactsSchema{}
	empty := (&core.Artifacts{Entries: []*core.Artifact{{TypeName: "Container"}}}).FilterTypes([]string{"Check"}, false)
	for name, filter := range map[string]func(context.Context, *core.Artifacts, struct{ URI string }) (*core.Artifacts, error){
		"filterUri":  schema.filterURI,
		"withoutUri": schema.withoutURI,
	} {
		t.Run(name, func(t *testing.T) {
			selected, err := filter(t.Context(), empty, struct{ URI string }{"dag://github.com/acme/project@main:verify"})
			require.NoError(t, err)
			require.Empty(t, selected.Entries)
			_, err = filter(t.Context(), empty, struct{ URI string }{"dag://github.com/acme/project@:verify"})
			require.ErrorContains(t, err, "empty workspace or version")
		})
	}
}

func TestArtifactTypedConversion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entries    []*core.Artifact
		wantErr    string
		wantValues int
	}{
		{name: "unmarked changeset", entries: []*core.Artifact{{TypeName: "Changeset", Path: []string{"edit"}}}, wantValues: 1},
		{name: "empty"},
		{name: "reject mixed selection before evaluation", entries: []*core.Artifact{
			{TypeName: "Changeset", Path: []string{"edit"}},
			{TypeName: "Service", Path: []string{"serve"}},
		}, wantErr: "dag://serve is a Service, not changeset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, srv, _, _ := resolverOutputFixture(t)
			srv.InstallObject(dagql.NewClass[*core.Artifacts](srv))
			srv.InstallObject(dagql.NewClass[*core.Artifact](srv))
			srv.InstallObject(dagql.NewClass[*core.Changeset](srv))
			schema := &artifactsSchema{}
			dagql.Fields[*core.Artifacts]{
				dagql.Func("items", schema.items),
				dagql.NodeFunc("asChangesets", schema.asChangesets),
			}.Install(srv)
			evaluations := 0
			dagql.Fields[*core.Artifact]{dagql.Func("value", func(context.Context, *core.Artifact, struct{}) (*core.Changeset, error) {
				evaluations++
				return &core.Changeset{}, nil
			})}.Install(srv)
			dagql.Fields[*core.Query]{dagql.Func("selection", func(context.Context, *core.Query, struct{}) (*core.Artifacts, error) {
				return &core.Artifacts{Entries: tc.entries}, nil
			})}.Install(srv)
			var result dagql.ObjectResultArray[*core.Changeset]
			err := srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "selection"}, dagql.Selector{Field: "asChangesets"})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Len(t, result, tc.wantValues)
			}
			require.Equal(t, tc.wantValues, evaluations)
		})
	}
}

// TestArtifactScopeMixesWorkspaceAndBoundArtifacts covers selections that mix
// workspace artifacts with bound ones (an LLM's scope): the latter have no
// workspace and are rooted at a live value.
func TestArtifactScopeMixesWorkspaceAndBoundArtifacts(t *testing.T) {
	ctx, srv, cache, _ := resolverOutputFixture(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Workspace]{}))
	ws := resolverAttach(t, ctx, srv, cache, "ws", &core.Workspace{Address: "file:///ws", Cwd: "/"})
	otherWs := resolverAttach(t, ctx, srv, cache, "otherWs", &core.Workspace{Address: "file:///other", Cwd: "/"})
	liveA := resolverAttach(t, ctx, srv, cache, "liveA", &core.Directory{})
	liveB := resolverAttach(t, ctx, srv, cache, "liveB", &core.Directory{})
	boundAt := func(root dagql.AnyObjectResult) *core.Artifact {
		rootNode := &core.ModTreeNode{Name: "roster", RootValue: root, Parent: &core.ModTreeNode{}}
		return &core.Artifact{
			ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory", DimensionKeys: []*core.ArtifactDimensionKey{},
			Node: &core.ModTreeNode{Name: "members", Parent: rootNode},
		}
	}
	inWorkspace := func(ws dagql.ObjectResult[*core.Workspace]) *core.Artifact {
		return &core.Artifact{ModuleName: "app", Path: []string{"app", "build"}, TypeName: "Container", DimensionKeys: []*core.ArtifactDimensionKey{}, Workspace: ws}
	}
	schema := &artifactsSchema{}

	t.Run("uri", func(t *testing.T) {
		// One workspace plus bound artifacts still has a relative address.
		mixed := &core.Artifacts{Entries: []*core.Artifact{boundAt(liveA), inWorkspace(ws)}}
		uri, err := schema.uri(ctx, mixed, struct{}{})
		require.NoError(t, err)
		require.Equal(t, "dag://", uri)
		narrowed, err := schema.filterURI(ctx, mixed, struct{ URI string }{"dag://roster/**"})
		require.NoError(t, err)
		require.Len(t, narrowed.Entries, 1)
		uri, err = schema.uri(ctx, narrowed, struct{}{})
		require.NoError(t, err)
		require.Equal(t, "dag://roster/**", uri)
		// Two workspaces still do not.
		_, err = schema.uri(ctx, &core.Artifacts{Entries: []*core.Artifact{boundAt(liveA), inWorkspace(ws), inWorkspace(otherWs)}}, struct{}{})
		require.ErrorContains(t, err, "multiple workspaces")
	})

	t.Run("absolute addresses skip bound artifacts", func(t *testing.T) {
		bound := &core.Artifacts{Entries: []*core.Artifact{boundAt(liveA)}}
		selected, err := schema.filterURI(ctx, bound, struct{ URI string }{"dag://github.com/acme/project@main:roster/members"})
		require.NoError(t, err)
		require.Empty(t, selected.Entries)
		kept, err := schema.withoutURI(ctx, bound, struct{ URI string }{"dag://github.com/acme/project@main:roster/members"})
		require.NoError(t, err)
		require.Len(t, kept.Entries, 1)
	})

	t.Run("withArtifacts keeps each root's address once", func(t *testing.T) {
		left := &core.Artifacts{Entries: []*core.Artifact{boundAt(liveA), inWorkspace(ws)}}
		right := &core.Artifacts{Entries: []*core.Artifact{boundAt(liveA), boundAt(liveB), inWorkspace(ws)}}
		combined, err := left.WithArtifacts(right)
		require.NoError(t, err)
		// The same path rooted at another live value is another address.
		require.Len(t, combined.Entries, 3)
		without, err := combined.WithoutArtifacts(&core.Artifacts{Entries: []*core.Artifact{boundAt(liveB)}})
		require.NoError(t, err)
		require.Len(t, without.Entries, 2)
		require.Equal(t, liveA.Self(), without.Entries[1].BoundRoot().Unwrap())
	})
}
