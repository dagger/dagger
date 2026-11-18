package daggercmd

import (
	"context"
	"errors"
	"os"
	"testing"

	"dagger.io/dagger/core"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/stretchr/testify/require"
)

func TestClearConfirmsBeforeInterruptAndPinsTarget(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "clear"}[confirmed], func(t *testing.T) {
			s, alice, rt := focusedWorker(t)
			rt.setState(core.AgentStateRunning)
			h := &shellCallHandler{llmSession: s}
			ctx := context.WithValue(t.Context(), commandSubmissionKey{}, &commandSubmission{mode: modePrompt, target: alice})
			previous := Frontend
			t.Cleanup(func() { Frontend = previous })
			Frontend = &idtui.FrontendMock{HandleFormFunc: func(_ context.Context, form *huh.Form) error {
				field := form.GetFocusedField()
				require.Equal(t, false, field.GetValue())
				require.Contains(t, field.View(), "Clear alice's conversation?")
				_, interrupts, _ := rt.counts()
				require.Zero(t, interrupts, "confirmation must precede interruption")
				s.SetTarget(s.agents[0]) // moving focus must not redirect the confirmed clear
				if confirmed {
					field.Focus()
					field.Update(tea.KeyMsg{Type: tea.KeyLeft})
				}
				return nil
			}}
			require.NoError(t, h.clearModelInteractive(ctx))
			sent, interrupts, stops := rt.counts()
			require.Zero(t, sent)
			require.Zero(t, stops)
			if confirmed {
				require.Equal(t, 1, interrupts)
				require.Equal(t, 1, rt.reseedCount())
				require.Equal(t, []string{"interrupt", "reseed"}, rt.verbLog())
			} else {
				require.Zero(t, interrupts)
				require.Zero(t, rt.reseedCount())
			}
			require.Same(t, rt, alice.runtime())
			require.Empty(t, runtimeOf(t, s.agents[0]).verbLog())
		})
	}
}

func TestControlPickersKeepSubmissionTarget(t *testing.T) {
	for _, command := range []string{"model", "effort"} {
		t.Run(command, func(t *testing.T) {
			dag, _ := newChainDag(t, map[string]any{"model": "test-model", "reasoningEffort": "low"})
			s, agents := testSession(t, "main", "worker")
			s.dag = dag
			for _, a := range agents {
				a.llm = core.Ref[*core.LLM](core.NewQuery(dag), "seed-"+core.ID(a.Name()))
				a.model, a.provider = "test-model", "local"
				runtimeOf(t, a).snapshot = "snapshot-" + core.ID(a.Name())
			}
			target := agents[1]
			s.SetTarget(target)
			h := &shellCallHandler{llmSession: s, llmModel: "main-model"}
			ctx := context.WithValue(t.Context(), commandSubmissionKey{}, &commandSubmission{mode: modePrompt, target: target})
			ctx = context.WithValue(ctx, llmModelConfigKey{}, llmModelConfigReader(func(context.Context) (*engine.LLMConfig, error) {
				return &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"local": {Model: "next-model"}}}, nil
			}))
			previous := Frontend
			t.Cleanup(func() { Frontend = previous })
			Frontend = &idtui.FrontendMock{HandleFormFunc: func(_ context.Context, form *huh.Form) error {
				s.SetTarget(agents[0])
				field := form.GetFocusedField()
				field.Focus()
				field.Update(tea.KeyMsg{Type: tea.KeyDown})
				return nil
			}}
			var err error
			if command == "model" {
				err = h.selectModelInteractive(ctx)
			} else {
				err = h.selectEffortInteractive(ctx)
			}
			require.NoError(t, err)
			require.Equal(t, []string{"beginEdit", "commitEdit"}, runtimeOf(t, target).verbLog())
			require.Empty(t, runtimeOf(t, agents[0]).verbLog())
			require.Equal(t, "main-model", h.llmModel, "background command must not overwrite focused model bookkeeping")
		})
	}
}

func TestCanceledModelBoundaryDoesNotSaveDefault(t *testing.T) {
	useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{DefaultProvider: "local", DefaultModel: "original"}})
	before, err := os.ReadFile(llmconfig.ConfigFile)
	require.NoError(t, err)
	a, baseRT, _ := idleAgent(t, map[string]any{"model": "test-model"})
	rt := &controlledEditRuntime{fakeRuntime: baseRT}
	a.bindRuntime(rt, "same-agent", "", true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rt.begin = func(ctx context.Context, _ string) (core.ID, error) {
		cancel()
		return "", context.Cause(ctx)
	}
	ctx = context.WithValue(ctx, llmModelConfigKey{}, llmModelConfigReader(func(context.Context) (*engine.LLMConfig, error) {
		return &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"local": {Model: "next-model"}}}, nil
	}))
	ctx = context.WithValue(ctx, llmConfigUpdaterKey{}, llmConfigUpdater(func(context.Context, *engine.LLMConfig) error {
		t.Fatal("canceled pending model change must not persist/reload defaults")
		return nil
	}))
	previous := Frontend
	t.Cleanup(func() { Frontend = previous })
	Frontend = &idtui.FrontendMock{HandleFormFunc: func(_ context.Context, form *huh.Form) error {
		field := form.GetFocusedField()
		field.Focus()
		field.Update(tea.KeyMsg{Type: tea.KeyDown})
		form.Update(huh.NextField())
		confirm := form.GetFocusedField()
		confirm.Focus()
		confirm.Update(tea.KeyMsg{Type: tea.KeyLeft})
		require.Equal(t, true, confirm.GetValue())
		return nil
	}}
	err = (&shellCallHandler{llmSession: a.session}).selectModelInteractive(ctx)
	require.True(t, errors.Is(err, context.Canceled))
	after, err := os.ReadFile(llmconfig.ConfigFile)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Same(t, rt, a.runtime())
	require.Equal(t, []string{"abortEdit"}, baseRT.verbLog())
}
