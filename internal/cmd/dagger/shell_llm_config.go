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
		command(".logout", "Choose a provider whose saved credentials to remove", NoArgs, c.logout),
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
		// Custom endpoints can carry credentials in userinfo, query or path.
		provider.BaseURL = redact(provider.BaseURL)
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

const logoutScopeNotice = "Environment, .env, and inherited credentials are not removed and may still provide access."

func (c *llmConfigCommands) logout(ctx context.Context, _ []string) error {
	cfg, err := llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil || len(cfg.LLM.Providers) == 0 {
		return c.print(ctx, "No saved LLM providers to log out from. Run /setup to configure one.\n"+logoutScopeNotice)
	}

	var options []huh.Option[string]
	for _, name := range slices.Sorted(maps.Keys(cfg.LLM.Providers)) {
		provider := cfg.LLM.Providers[name]
		label := name
		if provider.IsOAuth() {
			subscription := llmconfig.SubscriptionLabel(provider.SubscriptionType)
			if subscription == "" {
				subscription = "OAuth"
			}
			label += " (" + subscription + ")"
		}
		if !provider.Enabled {
			label += " (disabled)"
		}
		options = append(options, huh.NewOption(label, name))
	}
	var provider string
	form := idtui.NewForm(huh.NewGroup(huh.NewSelect[string]().
		Title("Log out from a saved LLM provider").
		Description(logoutScopeNotice).
		Height(min(len(options)+2, 12)).
		Filtering(true).
		Options(options...).
		Value(&provider)))
	if err := c.prompt.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, llmconfig.ErrAborted) {
			return nil
		}
		return err
	}
	if form.State == huh.StateAborted || provider == "" {
		return nil
	}

	// Re-read after the picker: another process may have updated other providers
	// while it was open. Do not overwrite their latest saved configuration.
	cfg, err = llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		return c.print(ctx, "No saved LLM providers to log out from.\n"+logoutScopeNotice)
	}
	if _, ok := cfg.LLM.Providers[provider]; !ok {
		return c.print(ctx, fmt.Sprintf("No saved credentials remain for %s.\n%s", provider, logoutScopeNotice))
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
	return c.changed(ctx, fmt.Sprintf("Saved credentials for %s removed.\n%s", provider, logoutScopeNotice))
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
		return fmt.Errorf("provider %q not configured, run /setup first", provider)
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
	form := idtui.NewForm(huh.NewGroup(idtui.NewExplicitConfirm("Delete", "Cancel", &confirmed).
		Title("Delete all stored LLM credentials?").
		Description("Environment variables and .env credentials are not changed.")))
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
