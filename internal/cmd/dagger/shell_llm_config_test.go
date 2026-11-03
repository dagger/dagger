package daggercmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/stretchr/testify/require"
)

type llmConfigPromptFunc func(context.Context, *huh.Form) error

func (f llmConfigPromptFunc) HandleForm(ctx context.Context, form *huh.Form) error {
	return f(ctx, form)
}

func newTestLLMConfigCommands(t *testing.T, prompt llmConfigPromptFunc) (*llmConfigCommands, *strings.Builder, *int) {
	t.Helper()
	var output strings.Builder
	reloads := 0
	if prompt == nil {
		prompt = func(context.Context, *huh.Form) error {
			t.Fatal("unexpected configuration form")
			return nil
		}
	}
	return &llmConfigCommands{
		prompt: prompt,
		reload: func(context.Context) error { reloads++; return nil },
		print: func(_ context.Context, text string) error {
			output.WriteString(text)
			return nil
		},
	}, &output, &reloads
}

func runLLMConfigCommand(t *testing.T, commands *llmConfigCommands, name string, args ...string) error {
	t.Helper()
	for _, command := range commands.builtins() {
		if command.Name() == name {
			if err := command.Args(args); err != nil {
				return err
			}
			return command.Run(t.Context(), command, args, nil)
		}
	}
	t.Fatalf("configuration command %s not found", name)
	return nil
}

func TestLLMConfigCommandArguments(t *testing.T) {
	commands, _, reloads := newTestLLMConfigCommands(t, nil)
	var names []string
	for _, command := range commands.builtins() {
		require.Equal(t, NoState, command.State)
		require.Equal(t, "llm", command.GroupID)
		names = append(names, command.Name())
		require.Error(t, runLLMConfigCommand(t, commands, command.Name(), "extra"))
	}
	require.ElementsMatch(t, []string{".setup", ".logout"}, names)
	require.Zero(t, *reloads)
	for _, command := range rootCmd.Commands() {
		require.NotEqual(t, "llm", command.Name(), "obsolete CLI command is still registered")
	}
}

func TestLLMConfigSetupSummaryHidesSecrets(t *testing.T) {
	secrets := []string{"literal-secret-token", "oauth-access-token", "oauth-refresh-token", "env://PRIVATE_KEY", "https://user:password@proxy.example/private-path?key=private-token"}
	original := &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
		"anthropic": {AuthType: "oauth", AuthToken: secrets[1], RefreshToken: secrets[2], SubscriptionType: "max", Enabled: true},
		"openai":    {APIKey: secrets[0], Enabled: true},
		"google":    {APIKey: secrets[3], Enabled: true},
		"local":     {APICompat: "openai", BaseURL: secrets[4], APIKey: secrets[0], Enabled: true},
	}}}
	useTempLLMConfig(t, original)
	commands, output, reloads := newTestLLMConfigCommands(t, func(_ context.Context, form *huh.Form) error {
		field, ok := form.GetFocusedField().(*huh.Select[string])
		require.True(t, ok)
		view := field.Height(100).View()
		for _, secret := range secrets {
			require.NotContains(t, view, secret)
		}
		require.NotContains(t, view, "lite…oken", "even token fragments should be hidden")
		require.Contains(t, view, "Claude Max")
		require.Contains(t, view, "API key connected")
		require.Contains(t, view, "Custom endpoint connected")
		require.Contains(t, view, "Checkmarks show saved connections")
		return huh.ErrUserAborted
	})
	require.NoError(t, runLLMConfigCommand(t, commands, ".setup"))
	require.Contains(t, output.String(), "cancelled")
	require.Zero(t, *reloads)
	loaded, err := llmconfig.Load()
	require.NoError(t, err)
	require.Equal(t, original, loaded)
}

func selectLogoutProvider(t *testing.T, index int) llmConfigPromptFunc {
	t.Helper()
	return func(_ context.Context, form *huh.Form) error {
		field := form.GetFocusedField()
		_, ok := field.(*huh.Select[string])
		require.True(t, ok, "logout must offer a provider picker")
		field.Focus()
		for range index {
			field.Update(tea.KeyMsg{Type: tea.KeyDown})
		}
		field.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return nil
	}
}

func TestLLMConfigLogout(t *testing.T) {
	for index, provider := range []string{"anthropic", "custom", "openai"} {
		t.Run(provider, func(t *testing.T) {
			useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{
				DefaultProvider: "openai", DefaultModel: "gpt-kept",
				Providers: map[string]llmconfig.Provider{
					"openai":    {APIKey: "keep-secret", Enabled: true},
					"anthropic": {AuthType: "oauth", AuthToken: "private-access", RefreshToken: "private-refresh", SubscriptionType: "max", Enabled: true},
					"custom":    {APIKey: "disabled-secret", Enabled: false},
				},
			}})
			commands, output, reloads := newTestLLMConfigCommands(t, func(ctx context.Context, form *huh.Form) error {
				view := form.GetFocusedField().View()
				for _, secret := range []string{"keep-secret", "private-access", "private-refresh", "disabled-secret"} {
					require.NotContains(t, view, secret)
				}
				require.Contains(t, view, "anthropic ("+llmconfig.SubscriptionLabel("max")+")")
				require.Contains(t, view, "custom (disabled)")
				require.Contains(t, view, "inherited credentials are not removed")
				return selectLogoutProvider(t, index)(ctx, form)
			})
			require.NoError(t, runLLMConfigCommand(t, commands, ".logout"))
			cfg, err := llmconfig.Load()
			require.NoError(t, err)
			require.NotContains(t, cfg.LLM.Providers, provider, "picker must be sorted by provider name")
			require.Len(t, cfg.LLM.Providers, 2)
			if provider == "openai" {
				require.Empty(t, cfg.LLM.DefaultProvider)
				require.Empty(t, cfg.LLM.DefaultModel)
			} else {
				require.Equal(t, "openai", cfg.LLM.DefaultProvider)
				require.Equal(t, "gpt-kept", cfg.LLM.DefaultModel)
			}
			require.Contains(t, output.String(), "Saved credentials for "+provider+" removed.")
			require.Contains(t, output.String(), logoutScopeNotice)
			require.Equal(t, 1, *reloads)
		})
	}
}

func TestLLMConfigLogoutWithoutSavedProviders(t *testing.T) {
	for _, cfg := range []*llmconfig.Config{nil, {}} {
		useTempLLMConfig(t, cfg)
		commands, output, reloads := newTestLLMConfigCommands(t, nil)
		require.NoError(t, runLLMConfigCommand(t, commands, ".logout"))
		require.Contains(t, output.String(), "No saved AI provider connections")
		require.Contains(t, output.String(), logoutScopeNotice)
		require.Zero(t, *reloads)
	}
}

func TestLLMConfigLogoutPreservesConcurrentChanges(t *testing.T) {
	useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{"openai": {APIKey: "remove", Enabled: true}}}})
	commands, _, reloads := newTestLLMConfigCommands(t, func(ctx context.Context, form *huh.Form) error {
		cfg, err := llmconfig.Load()
		require.NoError(t, err)
		cfg.LLM.Providers["anthropic"] = llmconfig.Provider{APIKey: "new-key", Enabled: true}
		require.NoError(t, cfg.Save())
		return selectLogoutProvider(t, 0)(ctx, form)
	})
	require.NoError(t, runLLMConfigCommand(t, commands, ".logout"))
	cfg, err := llmconfig.Load()
	require.NoError(t, err)
	require.NotContains(t, cfg.LLM.Providers, "openai")
	require.Equal(t, "new-key", cfg.LLM.Providers["anthropic"].APIKey)
	require.Equal(t, 1, *reloads)
}

func TestLLMConfigFormsCancelled(t *testing.T) {
	for _, name := range []string{".setup", ".logout"} {
		t.Run(name, func(t *testing.T) {
			original := &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{"openai": {APIKey: "keep", Enabled: true}}}}
			useTempLLMConfig(t, original)
			commands, _, reloads := newTestLLMConfigCommands(t, func(context.Context, *huh.Form) error {
				return huh.ErrUserAborted
			})
			require.NoError(t, runLLMConfigCommand(t, commands, name))
			loaded, err := llmconfig.Load()
			require.NoError(t, err)
			require.Equal(t, original, loaded)
			require.Zero(t, *reloads)
		})
	}
}

func TestLLMConfigReloadFailure(t *testing.T) {
	useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{"openai": {Enabled: true}}}})
	commands, output, _ := newTestLLMConfigCommands(t, selectLogoutProvider(t, 0))
	failed := errors.New("session unavailable")
	commands.reload = func(context.Context) error { return failed }
	err := runLLMConfigCommand(t, commands, ".logout")
	require.ErrorIs(t, err, failed)
	require.Contains(t, err.Error(), "configuration saved")
	require.Empty(t, output.String(), "do not claim live reload succeeded")
	loaded, err := llmconfig.Load()
	require.NoError(t, err)
	require.Empty(t, loaded.LLM.Providers)
}
