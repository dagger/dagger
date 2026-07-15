package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/dagger/dagger/util/cleanups"
)

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
}

// applyLLMConfigEnv loads the persisted LLM config (written by `dagger llm
// setup`) and exports each enabled provider's credentials into the process
// environment under the conventional variable names, unless already set
// (explicit env vars always win). The engine's LLM router resolves these via
// env:// against the client.
//
// OAuth subscription tokens are refreshed first (client-side, and only when
// expired). Anthropic (Claude Code) OAuth is wired end-to-end; Codex is not
// yet, so its token is not exported.
func applyLLMConfigEnv() {
	// Refresh any expired OAuth tokens before exporting them.
	_ = llmconfig.RefreshOAuthTokensIfNeeded()
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return
	}
	setIfEmpty := func(key, val string) {
		if val == "" {
			return
		}
		if _, ok := os.LookupEnv(key); ok {
			return
		}
		os.Setenv(key, val)
	}
	// Honor the configured default model (`dagger llm set-default`), which lives
	// in cfg.LLM.DefaultModel/DefaultProvider rather than any provider's own
	// p.Model. The engine's router picks a model from the per-provider *_MODEL
	// vars (LLMRouter.DefaultModel), so export the default there; otherwise the
	// default is written to config but never reaches the engine, which then
	// falls back to its hardcoded default. Done before the provider loop so it
	// wins over a stale per-provider model; explicit env vars still win via
	// setIfEmpty.
	if cfg.LLM.DefaultModel != "" {
		switch cfg.LLM.DefaultProvider {
		case "anthropic":
			setIfEmpty("ANTHROPIC_MODEL", cfg.LLM.DefaultModel)
		case "openai", "openrouter":
			setIfEmpty("OPENAI_MODEL", cfg.LLM.DefaultModel)
		case "openai-codex":
			setIfEmpty("OPENAI_CODEX_MODEL", cfg.LLM.DefaultModel)
		case "google", "gemini":
			setIfEmpty("GEMINI_MODEL", cfg.LLM.DefaultModel)
		case "local":
			setIfEmpty("LOCAL_MODEL", cfg.LLM.DefaultModel)
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
			// engine's router picks up. Anthropic (Claude Code) and OpenAI
			// Codex (ChatGPT subscription) are wired through the engine.
			switch name {
			case "anthropic":
				setIfEmpty("ANTHROPIC_AUTH_TOKEN", p.AuthToken)
				setIfEmpty("ANTHROPIC_REASONING_EFFORT", p.ReasoningEffort)
			case "openai-codex":
				setIfEmpty("OPENAI_CODEX_AUTH_TOKEN", p.AuthToken)
				setIfEmpty("OPENAI_CODEX_MODEL", p.Model)
				setIfEmpty("OPENAI_CODEX_REASONING_EFFORT", p.ReasoningEffort)
			}
			continue
		}
		switch name {
		case "anthropic":
			setIfEmpty("ANTHROPIC_API_KEY", p.APIKey)
			setIfEmpty("ANTHROPIC_BASE_URL", p.BaseURL)
			setIfEmpty("ANTHROPIC_MODEL", p.Model)
			setIfEmpty("ANTHROPIC_REASONING_EFFORT", p.ReasoningEffort)
		case "openai":
			if name != openAISlotOwner {
				continue
			}
			setIfEmpty("OPENAI_API_KEY", p.APIKey)
			setIfEmpty("OPENAI_BASE_URL", p.BaseURL)
			setIfEmpty("OPENAI_MODEL", p.Model)
		case "google", "gemini":
			setIfEmpty("GEMINI_API_KEY", p.APIKey)
			setIfEmpty("GEMINI_BASE_URL", p.BaseURL)
			setIfEmpty("GEMINI_MODEL", p.Model)
			setIfEmpty("GEMINI_REASONING_EFFORT", p.ReasoningEffort)
		case "openrouter":
			// OpenRouter is OpenAI-compatible; route it through the OpenAI vars.
			if name != openAISlotOwner {
				continue
			}
			setIfEmpty("OPENAI_API_KEY", p.APIKey)
			setIfEmpty("OPENAI_MODEL", p.Model)
			base := p.BaseURL
			if base == "" {
				base = "https://openrouter.ai/api/v1"
			}
			setIfEmpty("OPENAI_BASE_URL", base)
		case "local":
			// A self-hosted, OpenAI- or Anthropic-compatible endpoint. The engine
			// tunnels to it through this client, so it need only be reachable from
			// here (e.g. Ollama on localhost).
			setIfEmpty("LOCAL_BASE_URL", p.BaseURL)
			setIfEmpty("LOCAL_MODEL", p.Model)
			setIfEmpty("LOCAL_API_COMPAT", p.APICompat)
			setIfEmpty("LOCAL_API_KEY", p.APIKey)
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

		// If this was the default provider, clear it
		if cfg.LLM.DefaultProvider == provider {
			cfg.LLM.DefaultProvider = ""
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
