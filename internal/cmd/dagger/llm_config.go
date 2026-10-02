package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/dagger/dagger/util/cleanups"
)

// oauthProviders are the subscription OAuth providers the CLI keeps fresh, by
// config-file name (which is also their wire name).
var oauthProviders = []string{"anthropic", "openai-codex"}

// assembleLLMConfig builds the LLM configuration this client sends to the
// engine, from the config file, the environment, and ./.env in the working
// directory (--workdir has already been applied to the process by then). See
// hack/designs/llm-config-transport.md.
//
// Commands that never touch an LLM must not break over LLM configuration, so
// a source that cannot be read is warned about and left out. A value that
// cannot be parsed (llmconfig.ErrMalformed) fails the command instead:
// dropping it would silently route differently from what the user asked for.
func assembleLLMConfig() (*engine.LLMConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // the same directory, by a relative name
	}
	cfg, warnings, err := llmconfig.Assemble(cwd)
	for _, w := range warnings {
		slog.Warn("ignoring unreadable LLM configuration", "error", w)
	}
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// backgroundOAuthRefresh is replaceable so the refresher's scheduling and
// terminal-error behavior can be tested without a provider endpoint.
var backgroundOAuthRefresh = func(ctx context.Context, provider string) error {
	_, err := llmconfig.RefreshOAuthProviderIfNeeded(ctx, provider)
	return err
}

// Timings for the background refresher. Vars, not consts, so tests can shrink
// them — the same reason llmconfig's endpoint URLs are vars.
var (
	// oauthRefreshLead is how far ahead of a token's true expiry the refresher
	// wakes up. It matches the margin the expiry check applies, so the wake-up
	// finds the token due rather than just short of it.
	oauthRefreshLead = 5 * time.Minute
	// oauthRefreshUnknownInterval is the poll interval for a provider whose
	// token endpoint never said when the token expires. Unknown must not mean
	// "hot loop", nor "never look again".
	oauthRefreshUnknownInterval = 10 * time.Minute
	// oauthRefreshMinDelay keeps a token that is already past its refresh point
	// (a failing endpoint, a lifetime shorter than the lead) from spinning the
	// loop.
	oauthRefreshMinDelay = 30 * time.Second
)

// startOAuthTokenRefresher runs a goroutine that refreshes each enabled
// subscription OAuth provider shortly before its access token expires,
// persisting the rotated token to the config file. A `dagger shell` or
// `dagger agent` session easily outlives an hour-long access token, and
// refreshing ahead of expiry keeps the round-trip off the critical path: the
// engine's next llmconfig:// lookup already finds a fresh token.
//
// It returns a stop function, and is a no-op — no goroutine at all — unless a
// subscription provider is actually configured and enabled. The on-demand
// refresh in the llmconfig:// resolver remains the safety net; it is what
// covers a laptop that slept through the timer.
func startOAuthTokenRefresher(ctx context.Context) func() {
	providers := enabledOAuthProviders()
	if len(providers) == 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(nextOAuthRefreshDelay(providers)):
			}
			remaining := providers[:0]
			for _, provider := range providers {
				err := backgroundOAuthRefresh(ctx, provider)
				if err != nil && ctx.Err() == nil {
					slog.WarnContext(ctx, "failed to refresh LLM OAuth token",
						"provider", provider, "error", err)
					if llmconfig.IsTerminalOAuthRefreshError(err) {
						// invalid_grant and a missing refresh token cannot recover on
						// the next 30-second tick. Warn once and leave on-demand
						// refreshes to surface the reauthentication requirement.
						continue
					}
				}
				remaining = append(remaining, provider)
			}
			providers = remaining
			if len(providers) == 0 {
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// enabledOAuthProviders lists the configured, enabled subscription OAuth
// providers. One config read, no network — cheap enough to run for every
// command, including the ones that never talk to an LLM.
func enabledOAuthProviders() []string {
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return nil
	}
	var names []string
	for _, name := range oauthProviders {
		if p, ok := cfg.LLM.Providers[name]; ok && p.Enabled && p.IsOAuth() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// nextOAuthRefreshDelay returns how long to wait before the next refresh pass.
// It re-reads the persisted config on every cycle rather than arming once from
// a remembered expiry, so a token another dagger process refreshed — and the
// expiry it wrote — is respected here too.
func nextOAuthRefreshDelay(providers []string) time.Duration {
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return oauthRefreshUnknownInterval
	}
	var (
		delay time.Duration
		found bool
	)
	for _, name := range providers {
		p, ok := cfg.LLM.Providers[name]
		if !ok || !p.Enabled || !p.IsOAuth() {
			continue
		}
		next := oauthRefreshUnknownInterval
		if expiresAt := p.TokenExpiresAtTime(); !expiresAt.IsZero() {
			// May be negative for a token that is already overdue; the floor
			// below turns that into a prompt retry rather than a spin.
			next = time.Until(expiresAt.Add(-oauthRefreshLead))
		}
		if !found || next < delay {
			delay, found = next, true
		}
	}
	if !found {
		// Every provider was removed or disabled while we were sleeping. Keep
		// looking cheaply in case one comes back (`dagger llm setup` in another
		// terminal) rather than pinning the goroutine forever.
		return oauthRefreshUnknownInterval
	}
	return max(delay, oauthRefreshMinDelay)
}

func init() {
	rootCmd.AddCommand(llmParentCmd)
	llmParentCmd.AddCommand(
		llmConfigCmd,
		llmSetupCmd,
		llmAddKeyCmd,
		llmRemoveKeyCmd,
		llmSetDefaultCmd,
		llmResetCmd,
		llmShowConfigCmd,
	)

	// Serve the llmconfig:// credentials the LLMConfig we send refers to: API
	// keys stored in the config file or ./.env, and subscription OAuth tokens,
	// refreshed when due or when the engine reports the current one rejected.
	// The engine resolves them against this client, only for the provider it
	// actually routes, so commands that never touch an LLM pay nothing.
	secretprovider.RegisterLLMConfigResolver(llmconfig.ResolveSecret)
}

var llmParentCmd = &cobra.Command{
	Use:   "llm",
	Short: "Manage LLM configuration",
	Long:  "Manage LLM provider configuration, API keys, and default models.",
}

var llmConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Display current LLM configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := llmconfig.Load()
		if err != nil {
			return err
		}

		if cfg == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "No LLM configuration found.")
			fmt.Fprintln(cmd.OutOrStdout(), "Run 'dagger llm setup' to configure.")
			return nil
		}

		// Pretty-print with API keys redacted
		fmt.Fprintf(cmd.OutOrStdout(), "Default Provider: %s\n", cfg.LLM.DefaultProvider)
		if cfg.LLM.DefaultModel != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Default Model: %s\n", cfg.LLM.DefaultModel)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nConfigured Providers:\n")

		for name, provider := range cfg.LLM.Providers {
			if provider.Enabled {
				switch {
				case provider.IsOAuth():
					label := llmconfig.SubscriptionLabel(provider.SubscriptionType)
					if label == "" {
						label = "OAuth"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  %s %s: %s\n", idtui.IconSuccess, name, label)
				case provider.APICompat != "":
					fmt.Fprintf(cmd.OutOrStdout(), "  %s %s: %s (%s-compatible)\n", idtui.IconSuccess, name, provider.BaseURL, provider.APICompat)
				default:
					redacted := llmconfig.RedactKey(provider.APIKey)
					fmt.Fprintf(cmd.OutOrStdout(), "  %s %s: %s\n", idtui.IconSuccess, name, redacted)
				}
				if provider.BaseURL != "" && provider.APICompat == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "    Base URL: %s\n", provider.BaseURL)
				}
			}
		}

		fmt.Fprintf(cmd.OutOrStdout(), "\nConfig file: %s\n", llmconfig.ConfigFile)
		return nil
	},
}

var llmSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure LLM authentication interactively",
	RunE: func(cmd *cobra.Command, args []string) error {
		var configured bool
		var aborted bool
		err := Frontend.Run(cmd.Context(), opts, func(ctx context.Context) (cleanups.CleanupF, error) {
			// Shut the frontend's telemetry exporters down when setup returns so
			// the TUI sees EOF and exits. Unlike engine-backed commands, llm
			// setup has no telemetry stream to signal completion on its own, so
			// without this the TUI hangs after setup finishes. (Mirrors dagger
			// trace, which is likewise engine-less.)
			spanExp := Frontend.SpanExporter()
			defer spanExp.Shutdown(ctx)
			logExp := Frontend.LogExporter()
			defer logExp.Shutdown(ctx)

			var err error
			configured, err = llmconfig.InteractiveSetup(ctx, Frontend)
			if errors.Is(err, llmconfig.ErrAborted) {
				aborted = true
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return nil, nil
		})
		if err != nil {
			return err
		}
		if aborted {
			fmt.Fprintln(os.Stderr, "Setup cancelled.")
		} else if configured {
			fmt.Fprintln(os.Stderr, idtui.IconSuccess+" LLM configuration saved!")
		}
		return nil
	},
}

var llmAddKeyCmd = &cobra.Command{
	Use:   "add-key <provider>",
	Short: "Add or update API key for a provider",
	Long: `Add or update API key for a provider.

Supported providers:
  - openrouter: Unified access to 100+ models (https://openrouter.ai/keys)
  - anthropic: Claude models (https://console.anthropic.com/settings/keys)
  - openai: GPT models (https://platform.openai.com/api-keys)
  - google: Gemini models (https://aistudio.google.com/app/apikey)
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]

		// Validate provider name
		validProviders := []string{"openrouter", "anthropic", "openai", "google"}
		if !slices.Contains(validProviders, provider) {
			return fmt.Errorf("unsupported provider %q, must be one of: %s",
				provider, strings.Join(validProviders, ", "))
		}

		// Prompt for API key
		fmt.Fprintf(cmd.OutOrStdout(), "Enter API key for %s: ", provider)
		var apiKey string
		if _, err := fmt.Scanln(&apiKey); err != nil {
			return err
		}

		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			return fmt.Errorf("API key cannot be empty")
		}

		// Load or create config
		cfg, err := llmconfig.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			cfg = &llmconfig.Config{}
			cfg.LLM.DefaultProvider = provider
			cfg.LLM.Providers = make(map[string]llmconfig.Provider)
		}

		// Add or update provider
		providerCfg := llmconfig.Provider{
			APIKey:  apiKey,
			Enabled: true,
		}

		// Set BaseURL for OpenRouter
		if provider == "openrouter" {
			providerCfg.BaseURL = "https://openrouter.ai/api/v1"
		}

		cfg.LLM.Providers[provider] = providerCfg

		// If this is the first provider, set it as default
		if cfg.LLM.DefaultProvider == "" {
			cfg.LLM.DefaultProvider = provider
		}

		// Set default model if not set
		if cfg.LLM.DefaultModel == "" {
			switch provider {
			case "openrouter":
				cfg.LLM.DefaultModel = "anthropic/claude-sonnet-4.5"
			case "anthropic":
				cfg.LLM.DefaultModel = "claude-sonnet-4.5"
			case "openai":
				cfg.LLM.DefaultModel = "gpt-4.1"
			case "google":
				cfg.LLM.DefaultModel = "gemini-2.5-flash"
			}
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s API key for %s saved successfully!\n", idtui.IconSuccess, provider)
		return nil
	},
}

var llmRemoveKeyCmd = &cobra.Command{
	Use:   "remove-key <provider>",
	Short: "Remove API key for a provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]

		cfg, err := llmconfig.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no LLM configuration found")
		}

		if _, ok := cfg.LLM.Providers[provider]; !ok {
			return fmt.Errorf("provider %q not found in config", provider)
		}

		delete(cfg.LLM.Providers, provider)

		// If this was the default provider, clear it along with the default
		// model. The model belongs to the removed provider; leaving it set would
		// leave a default model with no provider, which the engine would then
		// route by its name alone — e.g. claude-sonnet-4.5 on an OpenAI key.
		// (llmSetDefaultCmd clears/rebinds the model for the same reason.)
		if cfg.LLM.DefaultProvider == provider {
			cfg.LLM.DefaultProvider = ""
			cfg.LLM.DefaultModel = ""
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s API key for %s removed.\n", idtui.IconSuccess, provider)
		return nil
	},
}

var llmSetDefaultCmd = &cobra.Command{
	Use:   "set-default <provider> [model]",
	Short: "Set default provider and optionally model",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]

		cfg, err := llmconfig.Load()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no LLM configuration found, run 'dagger llm setup' first")
		}

		// Verify provider exists
		providerCfg, ok := cfg.LLM.Providers[provider]
		if !ok {
			return fmt.Errorf("provider %q not configured, run 'dagger llm add-key %s' first",
				provider, provider)
		}

		cfg.LLM.DefaultProvider = provider
		if len(args) > 1 {
			cfg.LLM.DefaultModel = args[1]
		} else {
			// Don't carry the previous provider's model over: it would be
			// sent as this provider's default model and prefix routing could
			// send requests back to the old provider. Prefer the provider's own
			// configured model, then its catalog default; otherwise clear it.
			model := providerCfg.Model
			if model == "" {
				model = llmconfig.DefaultModelForProvider(provider)
			}
			cfg.LLM.DefaultModel = model
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s Default provider set to: %s\n", idtui.IconSuccess, provider)
		if cfg.LLM.DefaultModel != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s Default model set to: %s\n", idtui.IconSuccess, cfg.LLM.DefaultModel)
		}
		return nil
	},
}

var llmResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset LLM configuration (removes all stored credentials)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !llmconfig.ConfigExists() {
			fmt.Fprintln(cmd.OutOrStdout(), "No LLM configuration found.")
			return nil
		}

		// Confirm before deleting
		fmt.Fprint(cmd.OutOrStdout(), "This will delete all stored LLM credentials. Continue? [y/N]: ")
		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			return err
		}

		response = strings.ToLower(strings.TrimSpace(response))
		if response != "y" && response != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.")
			return nil
		}

		if err := llmconfig.Remove(); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), idtui.IconSuccess+" LLM configuration has been reset.")
		return nil
	},
}

var llmShowConfigCmd = &cobra.Command{
	Use:   "show-config",
	Short: "Show raw LLM configuration (JSON)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := llmconfig.Load()
		if err != nil {
			return err
		}

		if cfg == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "No LLM configuration found.")
			return nil
		}

		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	},
}
