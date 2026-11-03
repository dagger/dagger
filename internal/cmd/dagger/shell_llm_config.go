package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"dagger.io/dagger/core"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

func (h *shellCallHandler) llmConfigBuiltins() []*ShellCommand {
	return (&llmConfigCommands{
		prompt: Frontend,
		reload: h.reloadLLMConfig,
		print:  func(ctx context.Context, text string) error { return h.Print(ctx, text) },
		logoutTarget: func(ctx context.Context) (*sessionAgent, error) {
			// Logging out must not initialize an LLM just to discover that there
			// is no running conversation yet.
			h.llmL.Lock()
			initialized := h.llmSession != nil
			h.llmL.Unlock()
			if !initialized {
				return nil, nil
			}
			return h.commandTarget(ctx)
		},
	}).builtins()
}

// Keep configuration commands independent of engine initialization: /setup must
// work before the first LLM exists, and forms run in the already-active frontend.
type llmConfigCommands struct {
	prompt       llmconfig.PromptHandler
	reload       func(context.Context) error
	print        func(context.Context, string) error
	beforeLogout func(context.Context, string) (bool, error)
	logoutTarget func(context.Context) (*sessionAgent, error)
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
	// Capture the submitted target before opening the picker. A focus change
	// while either form is open must never redirect the eventual stop.
	beforeLogout := c.beforeLogout
	if c.logoutTarget != nil {
		target, err := c.logoutTarget(ctx)
		if err != nil {
			return err
		}
		beforeLogout = func(ctx context.Context, provider string) (bool, error) {
			return prepareProviderLogout(ctx, c.prompt, target, provider)
		}
	}
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
	if beforeLogout != nil {
		proceed, err := beforeLogout(ctx, provider)
		if err != nil || !proceed {
			return err
		}
	}

	// Re-read after the picker and any stop confirmation: another process may
	// have updated other providers while they were open. Preserve those updates.
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

func prepareProviderLogout(ctx context.Context, prompt llmconfig.PromptHandler, target *sessionAgent, provider string) (bool, error) {
	inUse, err := logoutProviderInUse(ctx, target, provider)
	if err != nil {
		return false, err
	}
	if !inUse {
		return true, nil
	}
	return confirmProviderLogout(ctx, prompt, provider, target.StopForLogout)
}

// Only the command's captured conversation is considered here. Other agents
// sharing this provider must never be stopped as a side effect of logout.
func logoutProviderInUse(ctx context.Context, target *sessionAgent, provider string) (bool, error) {
	if target == nil {
		return false, nil
	}
	rt := target.runtime()
	if rt == nil {
		return false, nil
	}
	state, err := rt.State(ctx)
	if err != nil {
		return false, fmt.Errorf("cannot check whether the selected agent is running: %w", err)
	}
	if state != core.AgentStateRunning && state != core.AgentStateWaitingInput {
		return false, nil
	}
	target.llmL.RLock()
	llm, cached := target.llm, target.provider
	target.llmL.RUnlock()
	if llm != nil {
		resolved, err := llm.Provider(ctx)
		if err == nil {
			return (modelSelection{Provider: resolved}).configRoute().Provider == provider, nil
		}
	}
	// A broken route must not prevent a safe, explicitly confirmed stop. But a
	// stale cached name cannot prove that some *other* provider is unused.
	if (modelSelection{Provider: cached}).configRoute().Provider == provider {
		return true, nil
	}
	return false, fmt.Errorf("cannot determine the running agent's provider; stop the agent and retry /logout")
}

func confirmProviderLogout(ctx context.Context, prompt llmconfig.PromptHandler, provider string, stop func(context.Context) error) (bool, error) {
	var confirmed bool
	form := idtui.NewForm(huh.NewGroup(idtui.NewExplicitConfirm("Stop and log out", "Cancel", &confirmed).
		Title(fmt.Sprintf("Stop the selected agent to log out of %s?", provider)).
		Description("This agent is using this provider. Its current work will stop before saved credentials are removed. Other agents will not be stopped.")))
	if err := prompt.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, llmconfig.ErrAborted) {
			return false, nil
		}
		return false, err
	}
	if form.State == huh.StateAborted || !confirmed {
		return false, nil
	}
	if err := stop(ctx); err != nil {
		return false, fmt.Errorf("could not stop the selected agent; credentials were not removed: %w", err)
	}
	return true, nil
}
