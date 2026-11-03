package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

func (h *shellCallHandler) llmConfigBuiltins() []*ShellCommand {
	return (&llmConfigCommands{
		prompt: Frontend,
		reload: h.reloadLLMConfig,
		print:  func(ctx context.Context, text string) error { return h.Print(ctx, text) },
	}).builtins()
}

// Keep configuration commands independent of engine initialization: /setup must
// work before the first LLM exists, and forms run in the already-active frontend.
type llmConfigCommands struct {
	prompt llmconfig.PromptHandler
	reload func(context.Context) error
	print  func(context.Context, string) error
}

func (c *llmConfigCommands) builtins() []*ShellCommand {
	command := func(use, description string, args PositionalArgs, run func(context.Context, []string) error) *ShellCommand {
		return &ShellCommand{
			Use: use, Description: description, Args: args, State: NoState, GroupID: "llm",
			Run: func(ctx context.Context, _ *ShellCommand, args []string, _ *ShellState) error {
				return run(ctx, args)
			},
		}
	}
	return []*ShellCommand{
		command(".setup", "Configure LLM authentication interactively", NoArgs, c.setup),
		command(".config", "Display saved LLM configuration (credentials redacted)", NoArgs, c.config),
		command(".show-config", "Show saved LLM configuration as JSON (credentials redacted)", NoArgs, c.showConfig),
		command(".add-key <provider>", "Add or update a provider's API key securely", ExactArgs(1), c.addKey),
		command(".remove-key <provider>", "Remove a provider's stored credentials", ExactArgs(1), c.removeKey),
		command(".set-default <provider> [model]", "Set the default provider and optionally model", func(args []string) error {
			if err := MinimumArgs(1)(args); err != nil {
				return err
			}
			return MaximumArgs(2)(args)
		}, c.setDefault),
		command(".reset", "Reset saved LLM configuration (removes stored credentials)", NoArgs, c.reset),
	}
}

func (c *llmConfigCommands) setup(ctx context.Context, _ []string) error {
	configured, err := llmconfig.InteractiveSetup(ctx, c.prompt)
	if errors.Is(err, llmconfig.ErrAborted) {
		return c.print(ctx, "Setup cancelled.")
	}
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	return c.changed(ctx, "LLM configuration saved!")
}

func (c *llmConfigCommands) changed(ctx context.Context, message string) error {
	if err := c.reload(ctx); err != nil {
		return fmt.Errorf("configuration saved, but failed to reload this session: %w", err)
	}
	return c.print(ctx, idtui.IconSuccess+" "+message)
}

func (c *llmConfigCommands) config(ctx context.Context, _ []string) error {
	cfg, err := llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		return c.print(ctx, "No LLM configuration found. Run /setup to configure.")
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Default Provider: %s\n", cfg.LLM.DefaultProvider)
	if cfg.LLM.DefaultModel != "" {
		fmt.Fprintf(&out, "Default Model: %s\n", cfg.LLM.DefaultModel)
	}
	fmt.Fprintln(&out, "\nConfigured Providers:")
	for _, name := range slices.Sorted(maps.Keys(cfg.LLM.Providers)) {
		provider := cfg.LLM.Providers[name]
		if !provider.Enabled {
			continue
		}
		label := "API key configured"
		switch {
		case provider.IsOAuth():
			label = llmconfig.SubscriptionLabel(provider.SubscriptionType)
			if label == "" {
				label = "OAuth"
			}
		case provider.APICompat != "":
			label = provider.APICompat + "-compatible"
		case provider.APIKey == "":
			label = "no API key"
		}
		fmt.Fprintf(&out, "  %s %s: %s\n", idtui.IconSuccess, name, label)
	}
	fmt.Fprintf(&out, "\nConfig file: %s", llmconfig.ConfigFile)
	return c.print(ctx, out.String())
}

func (c *llmConfigCommands) showConfig(ctx context.Context, _ []string) error {
	cfg, err := llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		return c.print(ctx, "No LLM configuration found. Run /setup to configure.")
	}
	// Slash-command output is retained in session telemetry. Never print even
	// partial credentials or secret references; read the file to inspect those.
	redact := func(value string) string {
		if value != "" {
			return "[REDACTED]"
		}
		return ""
	}
	for name, provider := range cfg.LLM.Providers {
		provider.APIKey = redact(provider.APIKey)
		provider.AuthToken = redact(provider.AuthToken)
		provider.RefreshToken = redact(provider.RefreshToken)
		provider.TokenExpiresAt = 0
		provider.TokenExpiry = 0
		cfg.LLM.Providers[name] = provider
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return c.print(ctx, string(data))
}

func (c *llmConfigCommands) addKey(ctx context.Context, args []string) error {
	provider := args[0]
	validProviders := []string{"openrouter", "anthropic", "openai", "google"}
	if !slices.Contains(validProviders, provider) {
		return fmt.Errorf("unsupported provider %q, must be one of: %s", provider, strings.Join(validProviders, ", "))
	}
	var apiKey string
	form := idtui.NewForm(huh.NewGroup(huh.NewInput().
		Title(fmt.Sprintf("Enter API key for %s", provider)).
		EchoMode(huh.EchoModePassword).
		Value(&apiKey).
		Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("API key cannot be empty")
			}
			return nil
		})))
	if err := c.prompt.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, llmconfig.ErrAborted) {
			return nil
		}
		return err
	}
	apiKey = strings.TrimSpace(apiKey)
	if form.State == huh.StateAborted || apiKey == "" {
		return nil
	}
	cfg, err := llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: make(map[string]llmconfig.Provider)}}
	}
	providerCfg := llmconfig.Provider{APIKey: apiKey, Enabled: true}
	if provider == "openrouter" {
		providerCfg.BaseURL = "https://openrouter.ai/api/v1"
	}
	cfg.LLM.Providers[provider] = providerCfg
	if cfg.LLM.DefaultProvider == "" {
		cfg.LLM.DefaultProvider = provider
	}
	// Adding another provider must not bind its model to the existing default.
	if cfg.LLM.DefaultProvider == provider && cfg.LLM.DefaultModel == "" {
		cfg.LLM.DefaultModel = llmconfig.DefaultModelForProvider(provider)
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return c.changed(ctx, fmt.Sprintf("API key for %s saved successfully!", provider))
}

func (c *llmConfigCommands) removeKey(ctx context.Context, args []string) error {
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
	// The default model belongs to the removed provider, not whichever
	// provider routing might choose next.
	if cfg.LLM.DefaultProvider == provider {
		cfg.LLM.DefaultProvider = ""
		cfg.LLM.DefaultModel = ""
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return c.changed(ctx, fmt.Sprintf("Credentials for %s removed.", provider))
}

func (c *llmConfigCommands) setDefault(ctx context.Context, args []string) error {
	provider := args[0]
	cfg, err := llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("no LLM configuration found, run /setup first")
	}
	providerCfg, ok := cfg.LLM.Providers[provider]
	if !ok {
		return fmt.Errorf("provider %q not configured, run /add-key %s or /setup first", provider, provider)
	}
	cfg.LLM.DefaultProvider = provider
	if len(args) > 1 {
		cfg.LLM.DefaultModel = args[1]
	} else {
		// Do not carry a previous provider's model to the new provider.
		cfg.LLM.DefaultModel = providerCfg.Model
		if cfg.LLM.DefaultModel == "" {
			cfg.LLM.DefaultModel = llmconfig.DefaultModelForProvider(provider)
		}
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	message := fmt.Sprintf("Default provider set to: %s", provider)
	if cfg.LLM.DefaultModel != "" {
		message += fmt.Sprintf("\nDefault model set to: %s", cfg.LLM.DefaultModel)
	}
	return c.changed(ctx, message)
}

func (c *llmConfigCommands) reset(ctx context.Context, _ []string) error {
	if !llmconfig.ConfigExists() {
		return c.print(ctx, "No LLM configuration found.")
	}
	var confirmed bool
	form := idtui.NewForm(huh.NewGroup(huh.NewConfirm().
		Title("Delete all stored LLM credentials?").
		Description("Environment variables and .env credentials are not changed.").
		Affirmative("Delete").Negative("Cancel").Value(&confirmed)))
	if err := c.prompt.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, llmconfig.ErrAborted) {
			return nil
		}
		return err
	}
	if form.State == huh.StateAborted || !confirmed {
		return c.print(ctx, "Cancelled.")
	}
	if err := llmconfig.Remove(); err != nil {
		return err
	}
	return c.changed(ctx, "LLM configuration has been reset.")
}
