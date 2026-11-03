package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

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
	command := func(use, description string, run func(context.Context, []string) error) *ShellCommand {
		return &ShellCommand{
			Use: use, Description: description, Args: NoArgs, State: NoState, GroupID: "llm",
			Run: func(ctx context.Context, _ *ShellCommand, args []string, _ *ShellState) error {
				return run(ctx, args)
			},
		}
	}
	return []*ShellCommand{
		command(".setup", "Connect an AI provider or update a saved connection", c.setup),
		command(".logout", "Choose an AI provider to disconnect from this device", c.logout),
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
	return c.changed(ctx, "AI provider connection saved!")
}

func (c *llmConfigCommands) changed(ctx context.Context, message string) error {
	if err := c.reload(ctx); err != nil {
		return fmt.Errorf("configuration saved, but failed to reload this session: %w", err)
	}
	return c.print(ctx, idtui.IconSuccess+" "+message)
}

const logoutScopeNotice = "Environment, .env, and inherited credentials are not removed and may still provide access."

func (c *llmConfigCommands) logout(ctx context.Context, _ []string) error {
	cfg, err := llmconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil || len(cfg.LLM.Providers) == 0 {
		return c.print(ctx, "No saved AI provider connections to remove. Run /setup to connect one.\n"+logoutScopeNotice)
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
		Title("Which AI provider do you want to disconnect?").
		Description("Removes its saved connection from this device. " + logoutScopeNotice).
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
		return c.print(ctx, "No saved AI provider connections to remove.\n"+logoutScopeNotice)
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
