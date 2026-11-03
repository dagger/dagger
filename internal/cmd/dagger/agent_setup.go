package daggercmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

type llmConfigUpdaterKey struct{}
type llmConfigUpdater func(context.Context, *engine.LLMConfig) error

type llmModelConfigKey struct{}
type llmModelConfigReader func(context.Context) (*engine.LLMConfig, error)

// reloadLLMConfig publishes a replacement, not an overlay: removing a provider
// must also remove its route from the running session. Credentials remain URIs.
func (h *shellCallHandler) reloadLLMConfig(ctx context.Context) error {
	update, ok := ctx.Value(llmConfigUpdaterKey{}).(llmConfigUpdater)
	if !ok {
		return fmt.Errorf("LLM configuration saved, but this session cannot reload it; restart dagger agent")
	}
	cfg, err := assembleLLMConfig()
	if err != nil {
		return err
	}
	if err := update(ctx, cfg); err != nil {
		return fmt.Errorf("LLM configuration saved, but could not reload it: %w", err)
	}
	// A failed initialization is retryable after credentials are configured.
	h.llmL.Lock()
	h.llmErr = nil
	h.llmL.Unlock()
	return nil
}

// ensureAgentLLMConfigured runs before composing agents, while the frontend is
// already available for forms. The engine checks inherited configuration too,
// so a nested CLI does not ask for credentials its parent already supplies.
// Cancellation exits cleanly without composing an unusable agent.
func ensureAgentLLMConfigured(
	ctx context.Context,
	interactive bool,
	configured func(context.Context) (bool, error),
	setup func(context.Context) (bool, error),
	reload func(context.Context) error,
) (bool, error) {
	ready, err := configured(ctx)
	if err != nil || ready {
		return ready, err
	}
	if !interactive {
		return false, fmt.Errorf("no LLM provider configured; run dagger agent in an interactive terminal to configure one with /setup")
	}
	ready, err = setup(ctx)
	if errors.Is(err, llmconfig.ErrAborted) {
		return false, nil
	}
	if err != nil || !ready {
		return false, err
	}
	if err := reload(ctx); err != nil {
		return false, err
	}
	return true, nil
}
