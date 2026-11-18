package schema

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestDirectoryWorkspaceModuleSourceProvenance(t *testing.T) {
	engine := newTransferTestEngine(t, nil)
	engine.srv.mainClient.InertAttachables = true
	ctx, srv := engine.base, engine.dag
	t.Cleanup(func() { require.NoError(t, engine.cache.CloseDiscardingPersistence()) })

	write := func(path, contents string) dagql.Selector {
		return dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
			{Name: "contents", Value: dagql.String(contents)},
		}}
	}
	const modulePath = "/tools/lint"
	const legacy = "dagger.json"
	const current = "dagger-module.toml"
	const currentConfig = "name = \"current\"\nengineVersion = \"v1.0.0-beta.28\"\n"
	fixture := []dagql.Selector{
		{Field: "directory"},
		write("README.md", "Unrelated documentation.\n"),
		write(modulePath+"/"+legacy, `{"name":"legacy","engineVersion":"v1.0.0-beta.28"}`),
		write(modulePath+"/source.txt", "Original source.\n"),
	}
	workspace := func(t *testing.T, edits ...dagql.Selector) dagql.ObjectResult[*core.Workspace] {
		t.Helper()
		selectors := append(append([]dagql.Selector{}, fixture...), edits...)
		selectors = append(selectors, dagql.Selector{Field: "asWorkspace"})
		var ws dagql.ObjectResult[*core.Workspace]
		require.NoError(t, srv.Select(ctx, srv.Root(), &ws, selectors...))
		require.IsType(t, &core.WorkspaceSourceDirectory{}, ws.Self().BaseSource())
		return ws
	}
	load := func(t *testing.T, ws dagql.ObjectResult[*core.Workspace], args ...dagql.NamedInput) dagql.ObjectResult[*core.ModuleSource] {
		t.Helper()
		var src dagql.ObjectResult[*core.ModuleSource]
		require.NoError(t, srv.Select(ctx, ws, &src, dagql.Selector{Field: "moduleSource", Args: args}))
		require.Equal(t, core.ModuleSourceKindDir, src.Self().Kind)
		return src
	}
	selectorsFor := func(t *testing.T, src dagql.ObjectResult[*core.ModuleSource]) []provenance.Selector {
		t.Helper()
		id, err := src.ID()
		require.NoError(t, err)
		facts, complete, err := engine.cache.ResultFacts(ctx, "main-session", &dagql.ResultCallRef{ResultID: id.EngineResultID()}, "workspace-inputs/v7")
		require.NoError(t, err)
		require.True(t, complete)
		require.Len(t, facts, 1)
		var fact workspaceSourceMetadataFact
		require.NoError(t, json.Unmarshal(facts[0].Payload, &fact))
		require.True(t, fact.Complete)
		require.Empty(t, fact.Issues)
		var selectors []provenance.Selector
		for _, read := range fact.Reads {
			selectors = append(selectors, read.Source.Selectors...)
		}
		return selectors
	}
	affects := func(t *testing.T, selectors []provenance.Selector, path string, expected bool) {
		t.Helper()
		match, err := provenance.AffectedByFile(selectors, path, nil)
		require.NoError(t, err)
		require.Equal(t, expected, match, path)
	}
	digest := func(t *testing.T, src dagql.ObjectResult[*core.ModuleSource]) dagql.String {
		t.Helper()
		var result dagql.String
		require.NoError(t, srv.Select(ctx, src, &result, dagql.Selector{Field: "digest"}))
		return result
	}

	ws := workspace(t)
	moduleArg := dagql.NamedInput{Name: "path", Value: dagql.String(modulePath)}
	original := load(t, ws, moduleArg)
	require.Equal(t, legacy, original.Self().ConfigFilename)
	originalDigest := digest(t, original)

	for _, result := range []dagql.ObjectResult[*core.ModuleSource]{original, load(t, ws, moduleArg)} {
		selectors := selectorsFor(t, result)
		affects(t, selectors, "/README.md", false)
		affects(t, selectors, modulePath+"/source.txt", true)
		affects(t, selectors, modulePath+"/"+current, true)
		affects(t, selectors, "/.gitignore", true)
		affects(t, selectors, "/tools/.gitignore", true)
		affects(t, selectors, "/tools/other/.gitignore", true)
		require.Contains(t, selectors, provenance.Selector{
			Path: modulePath + "/" + current, Include: []string{}, Exclude: []string{},
			Kind: provenance.WorkspaceRead, Origin: "Workspace.moduleSource",
		})
	}
	for _, edit := range []struct {
		name          string
		change        dagql.Selector
		manifest      string
		changesDigest bool
	}{
		{"README", write("README.md", "Edited unrelated documentation.\n"), legacy, false},
		{"source", write(modulePath+"/source.txt", "Edited source.\n"), legacy, true},
		{"higher-priority manifest", write(modulePath+"/"+current, currentConfig), current, true},
	} {
		t.Run(edit.name, func(t *testing.T) {
			src := load(t, workspace(t, edit.change), moduleArg)
			require.Equal(t, edit.manifest, src.Self().ConfigFilename)
			require.Equal(t, edit.changesDigest, digest(t, src) != originalDigest)
		})
	}

	t.Run("ignore rule symlink", func(t *testing.T) {
		ws := workspace(t, dagql.Selector{Field: "withSymlink", Args: []dagql.NamedInput{
			{Name: "target", Value: dagql.String("README.md")},
			{Name: "linkName", Value: dagql.String(".gitignore")},
		}})
		affects(t, selectorsFor(t, load(t, ws, moduleArg)), "/README.md", true)
	})
	t.Run("mounted manifest is not probed", func(t *testing.T) {
		var file dagql.Result[*core.File]
		require.NoError(t, srv.Select(ctx, srv.Root(), &file,
			dagql.Selector{Field: "directory"},
			write(current, currentConfig),
			dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(current)}}},
		))
		id, err := file.ID()
		require.NoError(t, err)
		var mounted dagql.ObjectResult[*core.Workspace]
		require.NoError(t, srv.Select(ctx, ws, &mounted, dagql.Selector{Field: "withMountedFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(modulePath + "/" + current)},
			{Name: "source", Value: dagql.NewID[*core.File](id)},
		}}))
		src := load(t, mounted, moduleArg)
		require.Equal(t, legacy, src.Self().ConfigFilename)
		affects(t, selectorsFor(t, src), "/README.md", true)
	})
	t.Run("missing manifest retains full context", func(t *testing.T) {
		src := load(t, ws,
			dagql.NamedInput{Name: "path", Value: dagql.String("/missing")},
			dagql.NamedInput{Name: "allowNotExists", Value: dagql.Boolean(true)},
		)
		require.False(t, src.Self().ConfigExists)
		contents, err := core.DirectoryReadFile(ctx, src.Self().ContextDirectory, "README.md")
		require.NoError(t, err)
		require.Equal(t, "Unrelated documentation.\n", string(contents))
		affects(t, selectorsFor(t, src), "/README.md", true)
	})
}
