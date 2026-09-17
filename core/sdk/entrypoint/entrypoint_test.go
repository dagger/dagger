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
