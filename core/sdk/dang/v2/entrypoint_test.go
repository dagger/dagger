package dangv2

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceRelativeEntrypointSource(t *testing.T) {
	t.Parallel()

	for _, source := range []string{".", "./entrypoint", ".dagger/modules/tiny", "../entrypoint", "github.com/dagger/dagger"} {
		require.True(t, isWorkspaceRelativeEntrypointSource(source), source)
	}
	for _, source := range []string{"/entrypoint", "https://example.com/repo.git", "git@example.com:repo.git", "module:source"} {
		require.False(t, isWorkspaceRelativeEntrypointSource(source), source)
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
