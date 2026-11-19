package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceInputsOnReusedEmptyDirectory(t *testing.T) {
	engine := newTransferTestEngine(t, nil)
	engine.srv.mainClient.InertAttachables = true
	ctx, srv := engine.base, engine.dag
	srv.ObserveCalls(core.ObserveWorkspaceInputs)
	t.Cleanup(func() { require.NoError(t, engine.cache.CloseDiscardingPersistence()) })

	// Make the fixture without filling the cache for Query.directory.
	dagql.Fields[*core.Query]{dagql.NodeFunc("fixtureDirectory", (&directorySchema{}).directory)}.Install(srv)
	write := func(path, contents string) dagql.Selector {
		return dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
			{Name: "contents", Value: dagql.String(contents)},
		}}
	}
	var root dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, srv.Root(), &root,
		dagql.Selector{Field: "fixtureDirectory"}, write("src/tool.go", "package tool\n"), write("README.md", "Fixture\n"),
	))
	var ws dagql.ObjectResult[*core.Workspace]
	require.NoError(t, srv.Select(ctx, root, &ws, dagql.Selector{Field: "asWorkspace"}))

	// This filtered read creates the empty directory used by the next call.
	var result dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, ws, &result, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.String(".")},
		{Name: "include", Value: dagql.ArrayInput[dagql.String]{"src/**"}},
	}}))
	empty := scratchSelect(t, ctx, srv)
	require.True(t, empty.HitCache())
	var source dagql.ObjectResult[*core.File]
	require.NoError(t, srv.Select(ctx, ws, &source, dagql.Selector{Field: "file", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.String("src/tool.go")},
	}}))
	sourceID, err := source.ID()
	require.NoError(t, err)
	var copied dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, empty, &copied, dagql.Selector{Field: "withFile", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.String("tool.go")},
		{Name: "source", Value: dagql.NewID[*core.File](sourceID)},
	}}))

	artifact := &core.Artifact{Workspace: ws, Node: &core.ModTreeNode{RootValue: copied}}
	inputs, err := artifact.Provenance(ctx)
	require.NoError(t, err)
	require.Empty(t, inputs.Issues)
	selected, err := inputs.AffectedByPath("src/tool.go", nil)
	require.NoError(t, err)
	require.True(t, selected)
	selected, err = inputs.AffectedByPath("README.md", nil)
	require.NoError(t, err)
	require.False(t, selected)
}
