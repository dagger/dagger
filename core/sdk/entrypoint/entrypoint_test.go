package entrypoint

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestSourceSubpath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		rootSubpath string
		source      string
		want        string
	}{
		{rootSubpath: ".dagger/modules/tiny", source: "entrypoint", want: ".dagger/modules/tiny/entrypoint"},
		{rootSubpath: ".dagger/modules/tiny", source: "./entrypoint", want: ".dagger/modules/tiny/entrypoint"},
		{rootSubpath: ".dagger/modules/tiny", source: ".", want: ".dagger/modules/tiny"},
		{rootSubpath: ".", source: "internal/dagger/entrypoint", want: "internal/dagger/entrypoint"},
		{rootSubpath: "", source: "entrypoint", want: "entrypoint"},
	} {
		got, err := sourceSubpath(&core.ModuleSource{SourceRootSubpath: tc.rootSubpath}, tc.source)
		require.NoError(t, err, tc.source)
		require.Equal(t, tc.want, got, tc.source)
	}

	for _, source := range []string{"/entrypoint", "../entrypoint", "entrypoint/../../other"} {
		_, err := sourceSubpath(&core.ModuleSource{SourceRootSubpath: ".dagger/modules/tiny"}, source)
		require.Error(t, err, source)
	}
}

func TestClassifySource(t *testing.T) {
	t.Parallel()

	// Values that the heuristics settle without a module context directory.
	src := dagql.ObjectResult[*core.ModuleSource]{}
	for source, want := range map[string]sourceKind{
		".":                          sourceKindLocal,
		"./entrypoint":               sourceKindLocal,
		"entrypoint":                 sourceKindLocal,
		"internal/dagger/entrypoint": sourceKindLocal,
		"/entrypoint":                sourceKindLocal,
		"../entrypoint":              sourceKindLocal,
		// A module reference resolves like runtime.source, with or without a
		// scheme, so a shared entrypoint can be named the way a runtime is.
		"github.com/dagger/python-sdk/entrypoint@v1":        sourceKindModuleRef,
		"github.com/dagger/dagger/modules/foo@v1.0.0":       sourceKindModuleRef,
		"https://github.com/dagger/dagger#main:modules/foo": sourceKindModuleRef,
		"git://git.example.com/team/repo#main:src":          sourceKindModuleRef,
		"ssh://git@github.com/dagger/dagger":                sourceKindModuleRef,
		// Address.directory keeps module:function and scp-style git.
		"module:source":                    sourceKindAddress,
		"module:sourceDir":                 sourceKindAddress,
		"git@github.com:dagger/dagger.git": sourceKindAddress,
	} {
		got, err := classifySource(t.Context(), src, source)
		require.NoError(t, err, source)
		require.Equal(t, want, got, source)
	}
}

func TestFunctionArgsJSON(t *testing.T) {
	t.Parallel()

	got, err := FunctionArgsJSON([]*core.FunctionCallArgValue{
		{Name: "name", Value: core.JSON(`"World"`)},
		{Name: "count", Value: core.JSON(`3`)},
		{Name: "optional", Value: core.JSON(`null`)},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"World","count":3,"optional":null}`, string(got))

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got, &decoded))
	require.JSONEq(t, `"World"`, string(decoded["name"]))
	require.JSONEq(t, `3`, string(decoded["count"]))
	require.JSONEq(t, `null`, string(decoded["optional"]))
}

func TestFunctionArgsJSONRejectsInvalidValue(t *testing.T) {
	t.Parallel()

	_, err := FunctionArgsJSON([]*core.FunctionCallArgValue{{Name: "bad", Value: core.JSON(`{`)}})
	require.EqualError(t, err, `function argument "bad" is not valid JSON`)
}

// The engine's own module loader attaches no workspace to a source, so the
// module's place in the workspace comes from host paths. That is the path
// every dagger call takes.
func TestModuleWorkspacePath(t *testing.T) {
	t.Parallel()

	ws := &core.Workspace{}
	ws.SetHostPath("/home/me/repo")

	local := func(contextDir, subpath string) *core.ModuleSource {
		return &core.ModuleSource{
			Kind:              core.ModuleSourceKindLocal,
			Local:             &core.LocalModuleSource{ContextDirectoryPath: contextDir},
			SourceRootSubpath: subpath,
		}
	}

	got, ok := moduleWorkspacePath(ws, local("/home/me/repo", ".dagger/modules/tiny"))
	require.True(t, ok)
	require.Equal(t, ".dagger/modules/tiny", got)

	// The workspace root can sit below the git root the context is loaded from.
	got, ok = moduleWorkspacePath(ws, local("/home/me", "repo/.dagger/modules/tiny"))
	require.True(t, ok)
	require.Equal(t, ".dagger/modules/tiny", got)

	// A module at the workspace root is ".".
	got, ok = moduleWorkspacePath(ws, local("/home/me/repo", ""))
	require.True(t, ok)
	require.Equal(t, ".", got)

	// A module outside the workspace has no place in it.
	_, ok = moduleWorkspacePath(ws, local("/home/me/other", "mod"))
	require.False(t, ok)

	// A git source's files are in its context directory, not the workspace.
	_, ok = moduleWorkspacePath(ws, &core.ModuleSource{Kind: core.ModuleSourceKindGit, SourceRootSubpath: "mod"})
	require.False(t, ok)

	// A remote or synthetic workspace has no host path to relate to.
	_, ok = moduleWorkspacePath(&core.Workspace{}, local("/home/me/repo", "mod"))
	require.False(t, ok)

	// A rootless workspace holds no files, even where its host path contains
	// the module.
	rootless := &core.Workspace{}
	rootless.SetHostPath("/home/me/repo")
	rootless.SetSource(core.NewWorkspaceSourceRootlessLocal("/home/me/repo"))
	_, ok = moduleWorkspacePath(rootless, local("/home/me/repo", "mod"))
	require.False(t, ok)
}

func TestValidateConstructorsRejectsObjectWithoutDefinition(t *testing.T) {
	t.Parallel()

	// TypeDef.withKind(OBJECT_KIND) yields exactly this value: the kind is set
	// and the object definition is not.
	err := validateConstructors([]*core.TypeDef{
		{Kind: core.TypeDefKindString},
		{Kind: core.TypeDefKindObject},
	})
	require.ErrorContains(t, err, "type 1 has kind OBJECT_KIND but defines no object")

	require.ErrorContains(t, validateConstructors([]*core.TypeDef{nil}), "type 0 is null")
	require.NoError(t, validateConstructors([]*core.TypeDef{{Kind: core.TypeDefKindString}}))
}
