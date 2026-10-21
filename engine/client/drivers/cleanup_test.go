package drivers

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanupOldEngines(t *testing.T) {
	unset := "<unset>"
	for _, tc := range []struct {
		name    string
		env     string
		config  string
		query   string
		cleanup bool
	}{
		{name: "default", env: unset, cleanup: true},
		{name: "url keeps", env: unset, query: "cleanup=false", cleanup: false},
		{name: "url cleans", env: unset, query: "cleanup=true", cleanup: true},
		{name: "config keeps", env: unset, config: "[engine]\nleave_old_engines = true\n", cleanup: false},
		{name: "config cleans", env: unset, config: "[engine]\nleave_old_engines = false\n", cleanup: true},
		{name: "config beats url", env: unset, config: "[engine]\nleave_old_engines = true\n", query: "cleanup=true", cleanup: false},
		{name: "config false beats url", env: unset, config: "[engine]\nleave_old_engines = false\n", query: "cleanup=false", cleanup: true},
		{name: "unparsable config falls through to url", env: unset, config: "[engine\n", query: "cleanup=false", cleanup: false},
		{name: "unparsable config falls through to default", env: unset, config: "[engine\n", cleanup: true},
		{name: "env keeps over config", env: "true", config: "[engine]\nleave_old_engines = false\n", cleanup: false},
		{name: "env cleans over config", env: "false", config: "[engine]\nleave_old_engines = true\n", cleanup: true},
		{name: "env beats url", env: "1", query: "cleanup=true", cleanup: false},
		// Set but unparsable: cleans up, as before the config file existed.
		{name: "empty env", env: "", config: "[engine]\nleave_old_engines = true\n", cleanup: true},
		{name: "invalid env", env: "yes please", config: "[engine]\nleave_old_engines = true\n", cleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if tc.config != "" {
				require.NoError(t, os.WriteFile(path, []byte(tc.config), 0o600))
			}
			t.Setenv("DAGGER_CONFIG", path)
			t.Setenv("DAGGER_LEAVE_OLD_ENGINE", tc.env)
			if tc.env == unset {
				require.NoError(t, os.Unsetenv("DAGGER_LEAVE_OLD_ENGINE"))
			}
			query, err := url.ParseQuery(tc.query)
			require.NoError(t, err)
			require.Equal(t, tc.cleanup, cleanupOldEngines(query))
		})
	}
}
