package dangv2

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestEntrypointSourceSubpath(t *testing.T) {
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
		got, err := entrypointSourceSubpath(&core.ModuleSource{SourceRootSubpath: tc.rootSubpath}, tc.source)
		require.NoError(t, err, tc.source)
		require.Equal(t, tc.want, got, tc.source)
	}

	for _, source := range []string{"/entrypoint", "../entrypoint", "entrypoint/../../other"} {
		_, err := entrypointSourceSubpath(&core.ModuleSource{SourceRootSubpath: ".dagger/modules/tiny"}, source)
		require.Error(t, err, source)
	}
}

func TestIsLocalEntrypointSource(t *testing.T) {
	t.Parallel()

	// Values that the fast heuristic settles without a module context directory.
	src := dagql.ObjectResult[*core.ModuleSource]{}
	for _, source := range []string{".", "./entrypoint", "entrypoint", "internal/dagger/entrypoint", "/entrypoint", "../entrypoint"} {
		local, err := isLocalEntrypointSource(t.Context(), src, source)
		require.NoError(t, err, source)
		require.True(t, local, source)
	}
	for _, source := range []string{
		"https://github.com/dagger/dagger#main:modules/foo",
		"ssh://git@github.com/dagger/dagger",
		"git@github.com:dagger/dagger.git",
		"module:source",
		"module:sourceDir",
		"github.com/dagger/dagger/modules/foo@v1.0.0",
	} {
		local, err := isLocalEntrypointSource(t.Context(), src, source)
		require.NoError(t, err, source)
		require.False(t, local, source)
	}
}

func TestFunctionArgsJSON(t *testing.T) {
	t.Parallel()

	got, err := functionArgsJSON([]*core.FunctionCallArgValue{
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

	_, err := functionArgsJSON([]*core.FunctionCallArgValue{{Name: "bad", Value: core.JSON(`{`)}})
	require.EqualError(t, err, `function argument "bad" is not valid JSON`)
}
