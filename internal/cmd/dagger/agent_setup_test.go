package daggercmd

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/stretchr/testify/require"
)

func TestEnsureAgentLLMConfigured(t *testing.T) {
	failure := errors.New("test failure")
	for _, tc := range []struct {
		name                                string
		configured, interactive, setupReady bool
		checkErr, setupErr, reloadErr       error
		wantReady, wantSetup, wantReload    bool
		wantErr                             string
	}{
		{name: "configured", configured: true, wantReady: true},
		{name: "first run", interactive: true, setupReady: true, wantReady: true, wantSetup: true, wantReload: true},
		{name: "cancelled", interactive: true, setupErr: llmconfig.ErrAborted, wantSetup: true},
		{name: "skipped", interactive: true, wantSetup: true},
		{name: "noninteractive", wantErr: "/setup"},
		{name: "check failed", checkErr: failure, wantErr: "test failure"},
		{name: "setup failed", interactive: true, setupErr: failure, wantSetup: true, wantErr: "test failure"},
		{name: "reload failed", interactive: true, setupReady: true, reloadErr: failure, wantSetup: true, wantReload: true, wantErr: "test failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var setupCalled, reloadCalled bool
			ready, err := ensureAgentLLMConfigured(t.Context(), tc.interactive,
				func(context.Context) (bool, error) { return tc.configured, tc.checkErr },
				func(context.Context) (bool, error) { setupCalled = true; return tc.setupReady, tc.setupErr },
				func(context.Context) error { reloadCalled = true; return tc.reloadErr },
			)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantReady, ready)
			require.Equal(t, tc.wantSetup, setupCalled)
			require.Equal(t, tc.wantReload, reloadCalled)
		})
	}
}

func TestReloadLLMConfig(t *testing.T) {
	clearLLMEnv(t)
	useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{
		Providers: map[string]llmconfig.Provider{"anthropic": {Enabled: true, APIKey: "test-key"}},
	}})
	h := &shellCallHandler{llmErr: errors.New("old initialization failure")}
	var updates []*engine.LLMConfig
	ctx := context.WithValue(t.Context(), llmConfigUpdaterKey{}, llmConfigUpdater(func(_ context.Context, cfg *engine.LLMConfig) error {
		updates = append(updates, cfg)
		return nil
	}))
	require.NoError(t, h.reloadLLMConfig(ctx))
	require.Nil(t, h.llmErr)
	require.Len(t, updates, 1)
	require.Equal(t, "llmconfig://anthropic/api_key", updates[0].Providers["anthropic"].APIKey)
	require.NoError(t, llmconfig.Remove())
	require.NoError(t, h.reloadLLMConfig(ctx))
	require.Len(t, updates, 2)
	require.True(t, updates[1].IsEmpty(), "removal must publish an empty replacement")

	require.ErrorContains(t, h.reloadLLMConfig(t.Context()), "restart dagger agent")
	h.llmErr = errors.New("keep this failure until reload succeeds")
	ctx = context.WithValue(t.Context(), llmConfigUpdaterKey{}, llmConfigUpdater(func(context.Context, *engine.LLMConfig) error { return errors.New("offline") }))
	require.ErrorContains(t, h.reloadLLMConfig(ctx), "offline")
	require.NotNil(t, h.llmErr)
}
