package engineutil

import (
	"os"
	"strings"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestSystemEnvFallback(t *testing.T) {
	const engineEnv = "_DAGGER_ENGINE_SYSTEMENV_DAGGER_TEST_SYSTEMENV"
	fallback := "from-fallback"
	for _, tc := range []struct {
		name      string
		entry     string
		container map[string]string
		engine    *string
		want      *string
	}{
		{name: "fallback when nothing sets it", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", &fallback), want: ptr("from-fallback")},
		{name: "engine wins over fallback", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", &fallback), engine: ptr("from-engine"), want: ptr("from-engine")},
		{name: "empty engine value wins over fallback", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", &fallback), engine: ptr(""), want: ptr("")},
		{name: "container wins over fallback", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", &fallback), container: map[string]string{"DAGGER_TEST_SYSTEMENV": "from-container"}},
		{name: "empty container value wins over fallback", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", &fallback), container: map[string]string{"DAGGER_TEST_SYSTEMENV": ""}},
		{name: "container wins over engine", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", &fallback), container: map[string]string{"DAGGER_TEST_SYSTEMENV": "from-container"}, engine: ptr("from-engine")},
		{name: "unset without fallback", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", nil)},
		{name: "engine without fallback", entry: SystemEnvEntry("DAGGER_TEST_SYSTEMENV", nil), engine: ptr("from-engine"), want: ptr("from-engine")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.engine != nil {
				t.Setenv(engineEnv, *tc.engine)
			} else {
				// t.Setenv restores the variable after the test
				t.Setenv(engineEnv, "")
				require.NoError(t, os.Unsetenv(engineEnv))
			}
			state := &execState{
				spec:       &specs.Spec{Process: &specs.Process{}},
				origEnvMap: tc.container,
				execMD:     &ExecutionMetadata{SystemEnvNames: []string{tc.entry}},
			}
			require.NoError(t, (&Client{}).setProxyEnvs(t.Context(), state))

			// Only values the executor adds land in the spec here; a
			// container's own value stays in the env it already has.
			var added []string
			for _, env := range state.spec.Process.Env {
				if name, val, _ := strings.Cut(env, "="); name == "DAGGER_TEST_SYSTEMENV" {
					added = append(added, val)
				}
			}
			if tc.want == nil {
				require.Empty(t, added)
			} else {
				require.Equal(t, []string{*tc.want}, added)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}
