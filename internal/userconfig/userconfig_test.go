package userconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/require"
)

func TestPath(t *testing.T) {
	t.Setenv("DAGGER_CONFIG", "")
	require.Equal(t, filepath.Join(xdg.ConfigHome, "dagger", "config.toml"), Path())

	t.Setenv("DAGGER_CONFIG", "/some/where/dagger.toml")
	require.Equal(t, "/some/where/dagger.toml", Path())
}

func TestLeaveOldEngines(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *string
		leave  bool
		ok     bool
	}{
		{name: "missing file"},
		{name: "empty file", config: ptr("")},
		{name: "unparsable file", config: ptr("[engine\nleave_old_engines = true\n")},
		{name: "other sections only", config: ptr("[llm]\ndefault_provider = \"anthropic\"\n")},
		{name: "engine section without key", config: ptr("[engine]\nother = 1\n")},
		{name: "wrong type", config: ptr("[engine]\nleave_old_engines = \"true\"\n")},
		{name: "engine is not a table", config: ptr("engine = true\n")},
		{name: "true", config: ptr("[engine]\nleave_old_engines = true\n"), leave: true, ok: true},
		{name: "false", config: ptr("[engine]\nleave_old_engines = false\n"), leave: false, ok: true},
		{
			name: "alongside other sections",
			config: ptr(`[llm]
default_provider = "anthropic"

[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"

[engine]
leave_old_engines = true
`),
			leave: true,
			ok:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if tc.config != nil {
				require.NoError(t, os.WriteFile(path, []byte(*tc.config), 0o600))
			}
			t.Setenv("DAGGER_CONFIG", path)
			leave, ok := LeaveOldEngines()
			require.Equal(t, tc.leave, leave)
			require.Equal(t, tc.ok, ok)
		})
	}
}

func TestLeaveOldEnginesUnreadable(t *testing.T) {
	// A directory where the file should be cannot be read as one.
	t.Setenv("DAGGER_CONFIG", t.TempDir())
	leave, ok := LeaveOldEngines()
	require.False(t, leave)
	require.False(t, ok)
}

func ptr(s string) *string { return &s }
