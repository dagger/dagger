package daggercmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSetupStepLogin(t *testing.T) {
	configFile := llmconfig.ConfigFile
	llmconfig.ConfigFile = filepath.Join(t.TempDir(), "config.toml")
	t.Cleanup(func() { llmconfig.ConfigFile = configFile })

	for _, tt := range []struct {
		name        string
		auth        *cloudauth.Cloud
		wantAlready bool
	}{
		{name: "not logged in"},
		{name: "logged in", auth: &cloudauth.Cloud{}, wantAlready: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetContext(ctx)
			cmd.SetOut(&out)

			err := setupStepLogin(ctx, cmd, func(context.Context) (*cloudauth.Cloud, error) {
				return tt.auth, nil
			}, nil)
			require.NoError(t, err)
			require.Equal(t, tt.wantAlready, bytes.Contains(out.Bytes(), []byte("Already logged in.")))
		})
	}
}

func TestConfirmSetupLoginSkipsNonInteractiveAutoApply(t *testing.T) {
	previousAutoApply := autoApply
	autoApply = true
	t.Cleanup(func() { autoApply = previousAutoApply })

	choice, err := confirmSetupLoginInteractive(t.Context(), &cobra.Command{}, nil, false)
	require.NoError(t, err)
	require.Equal(t, setupLoginNotNow, choice)
}

func TestDisableSetupCloudLoginPrompt(t *testing.T) {
	configFile := llmconfig.ConfigFile
	llmconfig.ConfigFile = filepath.Join(t.TempDir(), "config.toml")
	t.Cleanup(func() { llmconfig.ConfigFile = configFile })

	require.NoError(t, os.WriteFile(llmconfig.ConfigFile, []byte("[unrelated]\nvalue = 42\n"), 0o600))
	disabled, err := setupCloudLoginPromptDisabled()
	require.NoError(t, err)
	require.False(t, disabled)

	require.NoError(t, disableSetupCloudLoginPrompt())
	disabled, err = setupCloudLoginPromptDisabled()
	require.NoError(t, err)
	require.True(t, disabled)
	data, err := os.ReadFile(llmconfig.ConfigFile)
	require.NoError(t, err)
	require.Contains(t, string(data), "[unrelated]")
	require.Contains(t, string(data), `cloud_login = "never"`)

	require.NoError(t, clearSetupCloudLoginPromptPreference())
	disabled, err = setupCloudLoginPromptDisabled()
	require.NoError(t, err)
	require.False(t, disabled)
	data, err = os.ReadFile(llmconfig.ConfigFile)
	require.NoError(t, err)
	require.Contains(t, string(data), "[unrelated]")
	require.NotContains(t, string(data), "[setup]")
}

func TestSetupShowsFinalProgress(t *testing.T) {
	require.True(t, commandShowsFinalProgress(setupCmd))
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
