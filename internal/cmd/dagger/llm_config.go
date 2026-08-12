package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/dagger/dagger/util/cleanups"
)

// oauthEnvVars maps each subscription OAuth provider to the environment
// variables the engine resolves against the client: the bearer token, and the
// true access-token expiry (RFC 3339, UTC; absent or empty means unknown), so
// the engine can cache the credential until it actually expires. The token is
// refreshed on demand behind these lookups (see the env refresher registered
// below), so long-running sessions don't outlive it.
var oauthEnvVars = map[string]struct{ token, expiresAt string }{
	"anthropic":    {"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT"},
	"openai-codex": {"OPENAI_CODEX_AUTH_TOKEN", "OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT"},
}

// oauthEnvProviders maps either of a provider's variables back to the provider
// that owns it. The engine resolves the token first and the expiry second
// within one credential resolution and the refresher hook fires on each, so it
// has to answer to both names.
var oauthEnvProviders = func() map[string]string {
	providers := make(map[string]string, 2*len(oauthEnvVars))
	for provider, vars := range oauthEnvVars {
		providers[vars.token] = provider
		providers[vars.expiresAt] = provider
	}
	return providers
}()

// llmEnvExports records the values applyLLMConfigEnv and the OAuth refresher
// hook exported, keyed by variable name, so a refresh can update its own
// variables without clobbering one the user exported explicitly. An explicit
// `export ANTHROPIC_AUTH_TOKEN=...` wins for the whole session, not just at
// startup.
var (
	llmEnvMu      sync.Mutex
	llmEnvExports = map[string]string{}
)

// exportLLMEnv sets key to val unless the variable already holds a value we
// did not put there. Values we do export are remembered, so a later refresh
// can replace its own. An empty val exports nothing: callers pass whatever the
// config holds, and a provider that leaves a field unset must not undo a value
// exported for it earlier in the same pass (the default model, say).
func exportLLMEnv(key, val string) {
	if val == "" {
		return
	}
	llmEnvMu.Lock()
	defer llmEnvMu.Unlock()
	if cur, set := os.LookupEnv(key); set {
		if exported, ours := llmEnvExports[key]; !ours || exported != cur {
			return
		}
	}
	os.Setenv(key, val)
	llmEnvExports[key] = val
}

// clearLLMEnv drops a variable we exported earlier, leaving one the user
// exported explicitly alone.
func clearLLMEnv(key string) {
	llmEnvMu.Lock()
	defer llmEnvMu.Unlock()
	cur, set := os.LookupEnv(key)
	if !set {
		return
	}
	if exported, ours := llmEnvExports[key]; !ours || exported != cur {
		return
	}
	os.Unsetenv(key)
	delete(llmEnvExports, key)
}

// exportOAuthCredential exports a provider's bearer token together with the
// expiry that belongs to it. Together, always: the engine resolves the token
// and the expiry as two lookups of one credential, so leaving a stale expiry
// next to a fresh token would have it cache the new credential against the old
// deadline. An expiry we no longer know is cleared rather than left behind.
func exportOAuthCredential(provider string, p *llmconfig.Provider) {
	vars, ok := oauthEnvVars[provider]
	if !ok {
		return
	}
	// Never overwrite credentials the user exported explicitly, even when the
	// refresh was a no-op and these are just the stored values.
	exportLLMEnv(vars.token, p.AuthToken)
	if expiresAt := p.TokenExpiresAtRFC3339(); expiresAt != "" {
		exportLLMEnv(vars.expiresAt, expiresAt)
	} else {
		clearLLMEnv(vars.expiresAt)
	}
}

// exportOAuthEnv refreshes provider's token if it is due and exports the
// result.
func exportOAuthEnv(ctx context.Context, provider string) error {
	if _, ok := oauthEnvVars[provider]; !ok {
		return nil
	}
	p, err := llmconfig.RefreshOAuthProviderIfNeeded(ctx, provider)
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	exportOAuthCredential(provider, p)
	return nil
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
// subscription OAuth provider shortly before its access token expires and
// updates the exported token and expiry variables. A `dagger shell` or `dagger
// agent` session easily outlives an hour-long access token, and refreshing
// ahead of expiry keeps the round-trip off the critical path: the engine's
// next pull already finds a fresh token.
//
// It returns a stop function, and is a no-op — no goroutine at all — unless a
// subscription provider is actually configured and enabled. The on-demand
// refresher hook remains the safety net; it is what covers a laptop that slept
// through the timer.
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
			for _, provider := range providers {
				if err := exportOAuthEnv(ctx, provider); err != nil && ctx.Err() == nil {
					slog.WarnContext(ctx, "failed to refresh LLM OAuth token",
						"provider", provider, "error", err)
				}
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
	for name := range oauthEnvVars {
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
	// Export persisted API-key providers into the process environment so that
	// `dagger llm setup` takes effect for LLM usage (shell, call, etc.). The
	// engine's LLM router resolves these via env:// against the client.
	cobra.OnInitialize(applyLLMConfigEnv)

	// Keep subscription OAuth bearer tokens fresh for the life of the session.
	// applyLLMConfigEnv only refreshes and exports the token once, at startup;
	// the access token typically expires within the hour, so a long-running
	// session (dagger agent/shell, or a slow module) would keep sending an
	// expired token and get 401s. The engine re-resolves the token via
	// env://<KEY> against the client on each LLM router config load, so hook
	// that resolution: when the engine asks for an OAuth auth-token var, refresh
	// it if expired and update the process env before it's read.
	secretprovider.RegisterEnvRefresher(func(ctx context.Context, name string) error {
		provider, ok := oauthEnvProviders[name]
		if !ok {
			return nil
		}
		return exportOAuthEnv(ctx, provider)
	})
}

// applyLLMConfigEnv loads the persisted LLM config (written by `dagger llm
// setup`) and exports each enabled provider's credentials into the process
// environment under the conventional variable names, unless already set
// (explicit env vars always win). The engine's LLM router resolves these via
// env:// against the client.
//
// OAuth subscription tokens are refreshed first (client-side, and only when
// expired). Both Anthropic (Claude Code) and OpenAI Codex (ChatGPT) OAuth are
// wired end-to-end, so both tokens are exported.
func applyLLMConfigEnv() {
	// Refresh any expired OAuth tokens before exporting them. A failure here is
	// non-fatal (we fall back to whatever token is persisted), but warn so an
	// otherwise-silent 401 later on has a breadcrumb. cobra initializers get no
	// context; the refresh bounds itself with its own timeout.
	if err := llmconfig.RefreshOAuthTokensIfNeeded(context.Background()); err != nil {
		slog.Warn("failed to refresh LLM OAuth tokens", "error", err)
	}
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return
	}
	// Honor the configured default model (`dagger llm set-default`), which lives
	// in cfg.LLM.DefaultModel/DefaultProvider rather than any provider's own
	// p.Model. The engine's router picks a model from the per-provider *_MODEL
	// vars (LLMRouter.DefaultModel), so export the default there; otherwise the
	// default is written to config but never reaches the engine, which then
	// falls back to its hardcoded default. Done before the provider loop so it
	// wins over a stale per-provider model; explicit env vars still win via
	// exportLLMEnv.
	if cfg.LLM.DefaultModel != "" {
		switch cfg.LLM.DefaultProvider {
		case "anthropic":
			exportLLMEnv("ANTHROPIC_MODEL", cfg.LLM.DefaultModel)
		case "openai", "openrouter":
			exportLLMEnv("OPENAI_MODEL", cfg.LLM.DefaultModel)
		case "openai-codex":
			exportLLMEnv("OPENAI_CODEX_MODEL", cfg.LLM.DefaultModel)
		case "google", "gemini":
			exportLLMEnv("GEMINI_MODEL", cfg.LLM.DefaultModel)
		case "local":
			exportLLMEnv("LOCAL_MODEL", cfg.LLM.DefaultModel)
		}
	}
	// The openai and openrouter providers share the OPENAI_* variables. Pick a
	// single owner for that slot — the default provider if it is one of them,
	// otherwise openai — so map iteration order can't pair one provider's key
	// with the other's base URL.
	openAISlotOwner := ""
	for _, name := range []string{"openai", "openrouter"} {
		if p, ok := cfg.LLM.Providers[name]; ok && p.Enabled {
			if openAISlotOwner == "" || name == cfg.LLM.DefaultProvider {
				openAISlotOwner = name
			}
		}
	}
	for name, p := range cfg.LLM.Providers {
		if !p.Enabled {
			continue
		}
		if p.IsOAuth() {
			// OAuth subscription providers export a bearer token that the
			// engine's router picks up, plus the token's true expiry so the
			// engine can cache the credential exactly that long. Anthropic
			// (Claude Code) and OpenAI Codex (ChatGPT subscription) are wired
			// through the engine.
			exportOAuthCredential(name, &p)
			switch name {
			case "anthropic":
				exportLLMEnv("ANTHROPIC_REASONING_EFFORT", p.ReasoningEffort)
			case "openai-codex":
				exportLLMEnv("OPENAI_CODEX_MODEL", p.Model)
				exportLLMEnv("OPENAI_CODEX_REASONING_EFFORT", p.ReasoningEffort)
			}
			continue
		}
		switch name {
		case "anthropic":
			exportLLMEnv("ANTHROPIC_API_KEY", p.APIKey)
			exportLLMEnv("ANTHROPIC_BASE_URL", p.BaseURL)
			exportLLMEnv("ANTHROPIC_MODEL", p.Model)
			exportLLMEnv("ANTHROPIC_REASONING_EFFORT", p.ReasoningEffort)
		case "openai":
			if name != openAISlotOwner {
				continue
			}
			exportLLMEnv("OPENAI_API_KEY", p.APIKey)
			exportLLMEnv("OPENAI_BASE_URL", p.BaseURL)
			exportLLMEnv("OPENAI_MODEL", p.Model)
		case "google", "gemini":
			exportLLMEnv("GEMINI_API_KEY", p.APIKey)
			exportLLMEnv("GEMINI_BASE_URL", p.BaseURL)
			exportLLMEnv("GEMINI_MODEL", p.Model)
			exportLLMEnv("GEMINI_REASONING_EFFORT", p.ReasoningEffort)
		case "openrouter":
			// OpenRouter is OpenAI-compatible; route it through the OpenAI vars.
			if name != openAISlotOwner {
				continue
			}
			exportLLMEnv("OPENAI_API_KEY", p.APIKey)
			exportLLMEnv("OPENAI_MODEL", p.Model)
			base := p.BaseURL
			if base == "" {
				base = "https://openrouter.ai/api/v1"
			}
			exportLLMEnv("OPENAI_BASE_URL", base)
		case "local":
			// A self-hosted, OpenAI- or Anthropic-compatible endpoint. The engine
			// tunnels to it through this client, so it need only be reachable from
			// here (e.g. Ollama on localhost).
			exportLLMEnv("LOCAL_BASE_URL", p.BaseURL)
			exportLLMEnv("LOCAL_MODEL", p.Model)
			exportLLMEnv("LOCAL_API_COMPAT", p.APICompat)
			exportLLMEnv("LOCAL_API_KEY", p.APIKey)
		}
	}
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
		// bind it to whatever provider becomes default next, so applyLLMConfigEnv
		// would export e.g. OPENAI_MODEL=claude-sonnet-4.5. (llmSetDefaultCmd
		// clears/rebinds the model for the same reason.)
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
			// exported as this provider's model and prefix routing could send
			// requests back to the old provider. Prefer the provider's own
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
