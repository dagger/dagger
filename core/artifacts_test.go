package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type artifactAddressReceiver struct{}

func (*artifactAddressReceiver) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactAddressReceiver", NonNull: true}
}

func TestArtifactCurrentSpellingExecutesLegacyField(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "artifact-spelling", SessionID: "artifact-spelling"})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	installTypeDefTestClasses(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*artifactAddressReceiver]{}))
	calls := 0
	legacyField := LegacyNamer.FieldName("e2eTest")
	require.Equal(t, "e2ETest", legacyField)
	dagql.Fields[*artifactAddressReceiver]{dagql.Func(legacyField, func(context.Context, *artifactAddressReceiver, struct{}) (string, error) {
		calls++
		return "legacy-dispatch", nil
	})}.Install(srv)
	value, err := dagql.NewObjectResultForCall(&artifactAddressReceiver{}, srv, moduleObjectTestSyntheticCall("legacy-address-receiver", &artifactAddressReceiver{}))
	require.NoError(t, err)
	mod, err := dagql.NewResultForCall(&Module{NameField: "e2e-tests", SourceOverrides: &SourceOverrides{EngineVersion: "v1.0.0-beta.20"}}, moduleObjectTestSyntheticCall("legacy-address-module", &Module{}))
	require.NoError(t, err)
	obj := newTypeDefDetachedResult(t, srv, "legacy-address-object", NewObjectTypeDef("ArtifactAddressReceiver", "", nil))
	typ := newTypeDefDetachedResult(t, srv, "legacy-address-type", (&TypeDef{}).WithObjectTypeDef(obj))
	root := &ModTreeNode{Name: "e2e-tests", Parent: &ModTreeNode{}, RootValue: value, Type: typ, Module: dagql.ObjectResult[*Module]{Result: mod}, DagqlServer: srv}
	leaf := &ModTreeNode{Name: legacyField, Parent: root, DagqlServer: srv}
	require.Equal(t, []string{"e2e-tests", "e2e-test"}, leaf.CLIPath())
	require.Equal(t, ModTreePath{"e2e-tests", "e2ETest"}, leaf.Path(), "wire names remain versioned")
	all := &Artifacts{Entries: []*Artifact{{ModuleName: "e2e-tests", Path: leaf.CLIPath(), Node: leaf}}}
	selected, err := all.FilterPattern("e2e-tests/e2e-test")
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Equal(t, []string{"e2e-tests/e2e-test"}, selected.Selector.Paths)
	require.Equal(t, "dag://e2e-tests/e2e-test", selected.URI())
	requireRoundTrip(t, all, selected)
	var output dagql.String
	require.NoError(t, selected.Entries[0].Node.DagqlValue(ctx, &output))
	require.Equal(t, "legacy-dispatch", output.String())
	require.Equal(t, 1, calls)
	require.Equal(t, legacyField, leaf.Name)
	old, err := all.FilterPattern("e-2-e-tests/e-2-e-test")
	require.NoError(t, err)
	require.Empty(t, old.Entries, "no legacy artifact address aliases")
	root.WorkspaceMain = true
	require.Equal(t, []string{"e2e-test"}, leaf.CommandCLIPath())
}

func TestArtifactCollectionCurrentSpelling(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	collection, srv := collectionTestObject(t, TypeDefKindString, []any{"one"})
	collection.TypeDef.SourceModuleName = "e2e-tests"
	members, err := collection.TypeDef.CollectionMembers()
	require.NoError(t, err)
	members.Get.ReturnType.Self().AsObject.Value.Self().Name = "E2ETestsK8SCluster"
	mod, err := dagql.NewResultForCall(&Module{NameField: "e2e-tests", SourceOverrides: &SourceOverrides{EngineVersion: "v1.0.0-beta.20"}}, moduleObjectTestSyntheticCall("legacy-collection-module", &Module{}))
	require.NoError(t, err)
	module := dagql.ObjectResult[*Module]{Result: mod}
	obj := newTypeDefDetachedResult(t, srv, "address-collection-object", collection.TypeDef)
	typ := newTypeDefDetachedResult(t, srv, "address-collection-type", (&TypeDef{}).WithObjectTypeDef(obj))
	root := &ModTreeNode{Name: "e2e-tests", Module: module}
	node := &ModTreeNode{Name: "k8SClusters", Parent: root, Module: module, OriginalModule: module, Type: typ, Directives: []string{"check"}}
	var nodes []*ModTreeNode
	require.NoError(t, walkArtifactNodes(ctx, node, func(n *ModTreeNode) { nodes = append(nodes, n) }, map[string]bool{}))
	require.Len(t, nodes, 2, "finite collection and item schema")
	dim := nodes[1].CollectionDimension
	require.NotNil(t, dim)
	require.Equal(t, "/e2e-tests/k8s-clusters", dim.Identifier)
	require.Equal(t, "e2e-tests-k8s-cluster", dim.Name)
	require.Equal(t, "e2e-tests-k8s-clusters-k8s-cluster", dim.QualifiedName)
	require.Equal(t, "E2ETestsK8SCluster", dim.ItemType, "GraphQL type remains untouched")
	// Workspace discovery qualifies the tree after its dimensions are built.
	root.Parent = &ModTreeNode{}
	all := &Artifacts{}
	for _, n := range nodes {
		all.Entries = append(all.Entries, &Artifact{ModuleName: "e2e-tests", Path: n.CLIPath(), Node: n, TypeName: n.ObjectType().Name})
	}
	all.AllDimensions = all.DimensionDefinitions()
	selected, err := all.FilterPattern("e2e-tests/k8s-clusters")
	require.NoError(t, err)
	require.Equal(t, []string{"e2e-tests/k8s-clusters"}, selected.Selector.CollectionPaths)
	requireRoundTrip(t, all, selected)
	selected, err = selected.expand(ctx, func(context.Context, *Artifact) ([]collectionKey, error) {
		t.Fatal("a collection endpoint must not enumerate its items")
		return nil, nil
	})
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Equal(t, "Items", selected.Entries[0].TypeName)
}

func TestArtifactCanonicalPatterns(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{Path: []string{"e2e-tests", "e2e-test"}},
		{Path: []string{"e2e-tests", "k8s-cluster"}},
		{Path: []string{"other", "e2e-test"}},
	}}
	for _, tc := range []struct {
		pattern, recorded string
		count             int
	}{
		{"E2ETests/E2ETest", "e2e-tests/e2e-test", 1},
		{"e2e-tests/*", "e2e-tests/*", 2},
		{"{e2e-tests,other}/e2e-test", "{e2e-tests,other}/e2e-test", 2},
		{"e-2-e-tests/*", "e2-e-tests/*", 0},
		{"", "", 0},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			selected, err := all.FilterPattern(tc.pattern)
			require.NoError(t, err)
			require.Len(t, selected.Entries, tc.count)
			if tc.pattern == "" {
				require.NotNil(t, selected.Selector.Paths)
				require.Empty(t, selected.Selector.Paths)
			} else {
				require.Equal(t, []string{tc.recorded}, selected.Selector.Paths)
			}
			requireRoundTrip(t, all, selected)
		})
	}
	require.Equal(t, []string{"e2e-tests/k8s-cluster/**"}, IncludePatterns("E2ETests:K8sCluster"))
	require.Equal(t, []string{"{e2e-tests,other}/**"}, IncludePatterns("{e2e-tests,other}"))
	selected, err := all.FilterPattern("e2e-tests/*")
	require.NoError(t, err)
	selected, err = selected.FilterPattern("*/k8s-cluster")
	require.NoError(t, err)
	require.Equal(t, []string{"e2e-tests/k8s-cluster"}, selected.Selector.Paths)
	require.NotContains(t, selected.URI(), "{")
	requireRoundTrip(t, all, selected)
	empty, err := selected.FilterPattern("other/*")
	require.NoError(t, err)
	require.Empty(t, empty.Entries)
	requireRoundTrip(t, all, empty)
	for _, include := range []string{"e2e-tests", "E2ETests"} {
		patterns := IncludePatterns(include)
		var matched []string
		for _, entry := range all.Entries {
			match, err := matchesInclude(entry, patterns)
			require.NoError(t, err)
			if match {
				matched = append(matched, strings.Join(entry.Path, "/"))
			}
		}
		require.Equal(t, []string{"e2e-tests/e2e-test", "e2e-tests/k8s-cluster"}, matched)
	}
}

func TestArtifactQuestionMarkPatternURIRoundTrip(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{Path: []string{"scana"}, TypeName: "Check"},
		{Path: []string{"scanb"}, TypeName: "Check"},
		{Path: []string{"scanlong"}, TypeName: "Check"},
	}}
	selected, err := all.FilterPattern("scan?")
	require.NoError(t, err)
	require.Len(t, selected.Entries, 2)
	address, err := dagaddress.Parse(selected.URI())
	require.NoError(t, err)
	replayed, err := all.FilterURI(address)
	require.NoError(t, err)
	require.Equal(t, selected.Entries, replayed.Entries)
}

func TestArtifactAbsoluteURI(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	ref, err := dagql.NewResultForCall(&GitRef{Ref: &gitutil.Ref{SHA: commit}}, &dagql.ResultCall{})
	require.NoError(t, err)
	for _, remote := range []string{
		"https://github.com/acme/repo",
		"ssh://git@github.com/acme/repo",
		"git@github.com:acme/repo",
		"github.com/acme/repo",
	} {
		for _, subdir := range []string{"", "subdir"} {
			t.Run(remote+"/"+subdir, func(t *testing.T) {
				ws, err := dagql.NewResultForCall(&Workspace{
					Address: GitRefString(remote, subdir, "main"),
					source:  NewWorkspaceSourceGitRef(ref),
				}, &dagql.ResultCall{})
				require.NoError(t, err)
				artifact := &Artifact{
					Workspace: dagql.ObjectResult[*Workspace]{Result: ws},
					Path:      []string{"base"},
				}
				uri, err := artifact.URI(ArtifactURIOpts{Absolute: true})
				require.NoError(t, err)
				address := "github.com/acme/repo"
				if subdir != "" {
					address += "/" + subdir
				}
				require.Equal(t, "dag://"+address+"@"+commit+":base", uri)
			})
		}
	}
}

func TestArtifactWithoutWorkspace(t *testing.T) {
	bound := &Artifact{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory"}
	// No absolute address: it names a workspace.
	_, err := bound.URI(ArtifactURIOpts{Absolute: true})
	require.ErrorContains(t, err, "has no absolute address")
	uri, err := bound.URI(ArtifactURIOpts{DimensionKeys: true, TypeAssertion: true})
	require.NoError(t, err)
	require.Equal(t, "dag+directory://?directory=roster/members", uri)
	// Evaluated in the caller's context, which is left as is.
	ctx := t.Context()
	evalCtx, err := bound.WorkspaceContext(ctx)
	require.NoError(t, err)
	require.Equal(t, ctx, evalCtx)
	require.Nil(t, bound.BoundRoot())

	// An absolute address never selects an artifact without a workspace.
	ws, err := dagql.NewResultForCall(&Workspace{Address: "file:///ws"}, &dagql.ResultCall{})
	require.NoError(t, err)
	inWorkspace := &Artifact{ModuleName: "roster", Path: []string{"roster", "members"}, TypeName: "Directory", Workspace: dagql.ObjectResult[*Workspace]{Result: ws}}
	mixed := &Artifacts{Entries: []*Artifact{bound, inWorkspace}}
	absolute, err := dagaddress.Parse("dag://github.com/acme/repo@1111111111111111111111111111111111111111:roster/members")
	require.NoError(t, err)
	selected, err := mixed.FilterURI(absolute)
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Same(t, ws.Self(), selected.Entries[0].Workspace.Self())
	relative, err := dagaddress.Parse("dag://roster/members")
	require.NoError(t, err)
	selected, err = mixed.FilterURI(relative)
	require.NoError(t, err)
	require.Len(t, selected.Entries, 2)
}

func TestArtifactParentDirectivesAreImmediate(t *testing.T) {
	parent := &ModTreeNode{Directives: []string{"generate"}}
	child := &ModTreeNode{Parent: parent}
	grandchild := &ModTreeNode{Parent: child}
	artifacts := &Artifacts{Entries: []*Artifact{
		{Path: []string{"child"}, Node: child},
		{Path: []string{"child", "grandchild"}, Node: grandchild},
		{Path: []string{"load"}},
	}}
	included := artifacts.FilterParentDirectives([]string{"generate"}, false)
	require.Len(t, included.Entries, 1)
	require.Equal(t, []string{"child"}, included.Entries[0].Path)
	excluded := artifacts.FilterParentDirectives([]string{"generate"}, true)
	require.Len(t, excluded.Entries, 2)
	require.Equal(t, []string{"child", "grandchild"}, excluded.Entries[0].Path)
	require.Equal(t, []string{"load"}, excluded.Entries[1].Path)
}
