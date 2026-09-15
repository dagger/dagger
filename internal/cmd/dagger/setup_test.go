package daggercmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	toml "github.com/pelletier/go-toml"
	"github.com/stretchr/testify/require"
)

func TestClearSetupCloudLoginPromptPreference(t *testing.T) {
	for _, tt := range []struct {
		name      string
		config    string
		keepSetup bool
	}{
		{name: "no config"},
		{name: "no preference", config: "[unrelated]\nvalue = 42\n"},
		{name: "old preference", config: "[unrelated]\nvalue = 42\n[setup]\ncloud_login = 'never'\n"},
		{name: "other setup settings", config: "[unrelated]\nvalue = 42\n[setup]\ncloud_login = 'never'\nother = true\n", keepSetup: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configFile := llmconfig.ConfigFile
			llmconfig.ConfigFile = filepath.Join(t.TempDir(), "config.toml")
			t.Cleanup(func() { llmconfig.ConfigFile = configFile })

			if tt.config != "" {
				require.NoError(t, os.WriteFile(llmconfig.ConfigFile, []byte(tt.config), 0o600))
			}
			require.NoError(t, clearSetupCloudLoginPromptPreference())
			if tt.config == "" {
				require.NoFileExists(t, llmconfig.ConfigFile)
				return
			}
			data, err := os.ReadFile(llmconfig.ConfigFile)
			require.NoError(t, err)
			tree, err := toml.LoadBytes(data)
			require.NoError(t, err)
			require.EqualValues(t, 42, tree.GetPath([]string{"unrelated", "value"}))
			require.False(t, tree.HasPath([]string{"setup", "cloud_login"}))
			require.Equal(t, tt.keepSetup, tree.Has("setup"))
			if tt.keepSetup {
				require.Equal(t, true, tree.GetPath([]string{"setup", "other"}))
			}
		})
	}
}

func TestInitShowsFinalProgress(t *testing.T) {
	require.True(t, commandShowsFinalProgress(initCmd))
	require.False(t, commandShowsFinalProgress(setupCmd))
}

func TestFilterRecommendations(t *testing.T) {
	recs := []recommendation{
		{Module: registryModule{Name: "eslint", Repo: "github.com/dagger/eslint"}},
		{Module: registryModule{Name: "go", Repo: "github.com/dagger/go"}},
		{Module: registryModule{Name: "vitest", Repo: "github.com/dagger/vitest"}},
	}
	got := filterRecommendations(recs, []string{
		"github.com/dagger/vitest",
		"github.com/dagger/eslint",
	})
	require.Equal(t, []recommendation{recs[0], recs[2]}, got)
}

func TestSkippedRecommendations(t *testing.T) {
	require.Equal(t, "Recommended modules skipped.", skippedRecommendations())
}
