package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"

	enginecore "github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

func TestModelPickerCanReplaceUnavailableRoute(t *testing.T) {
	for _, tc := range []struct {
		name          string
		cached        bool
		modelReadable bool
	}{
		{name: "no cached route"},
		{name: "cached local route", cached: true},
		{name: "provider unavailable", cached: true, modelReadable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var queries []string
			dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
				var query dagger.Request
				require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
				queries = append(queries, query.Query)
				body := `{"errors":[{"message":"unsupported local API compatibility mode after logout"}]}`
				if strings.Contains(query.Query, "withModel") {
					body = `{"data":{"node":{"withModel":{"model":"gpt-5","provider":"openai"}}}}`
				} else if tc.modelReadable && strings.Contains(query.Query, "model") {
					body = `{"data":{"node":{"model":"old-local-model"}}}`
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}))
			require.NoError(t, err)
			t.Cleanup(func() { dag.Close() })
			s := &LLMSession{dag: dag, plumbingCtx: t.Context()}
			a := s.newAgent("test")
			a.llm = core.Ref[*core.LLM](core.NewQuery(dag), "logged-out-local")
			if tc.cached {
				a.model, a.provider = "old-local-model", "local"
			}
			s.target = a
			h := &shellCallHandler{llmSession: s}
			ctx := context.WithValue(t.Context(), llmModelConfigKey{}, llmModelConfigReader(func(context.Context) (*engine.LLMConfig, error) {
				return &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"openai": {Model: "gpt-5"}}}, nil
			}))
			previous := Frontend
			t.Cleanup(func() { Frontend = previous })
			forms := 0
			Frontend = &idtui.FrontendMock{HandleFormFunc: func(_ context.Context, form *huh.Form) error {
				forms++
				field := form.GetFocusedField()
				require.Equal(t, modelSelection{Provider: "openai", Model: "gpt-5"}, field.GetValue())
				if tc.cached {
					require.Contains(t, field.View(), "Current: old-local-model (local)")
				} else {
					require.Contains(t, field.View(), "Current model unavailable")
				}
				return nil
			}}
			require.NoError(t, h.selectModelInteractive(ctx))
			require.Equal(t, 1, forms)
			require.Equal(t, "gpt-5", a.model)
			require.Equal(t, "openai", a.provider)
			require.Contains(t, strings.Join(queries, "\n"), `provider:"openai"`)
		})
	}
}

func TestModelPickerNormalizesOtherProvider(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("OPENAI_BASE_URL", "http://custom-provider.test")
	useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
		"openai": {Enabled: true, Model: "qwen-old"},
	}}})
	cfg, err := assembleLLMConfig()
	require.NoError(t, err)
	router, err := enginecore.NewLLMRouter(cfg, &engine.ClientMetadata{})
	require.NoError(t, err)
	route, err := router.Route("qwen-new", "")
	require.NoError(t, err)
	require.Equal(t, enginecore.Other, route.Provider)
	current := modelSelection{Provider: string(route.Provider), Model: route.Model}
	options := modelOptions(cfg, current)
	var selected modelSelection
	var useDefault bool
	form := newModelPickerForm(options, current, &selected, &useDefault)
	require.Equal(t, modelSelection{Provider: "openai", Model: "qwen-new"}, selected)
	require.Contains(t, form.GetFocusedField().View(), "qwen-new (openai) (current)")
	require.Contains(t, form.GetFocusedField().View(), "qwen-old (openai)")
	require.NotContains(t, form.GetFocusedField().View(), "(other)")
	for _, option := range options {
		require.Equal(t, "openai", option.Value.Provider)
	}

	ctx := context.WithValue(t.Context(), llmConfigUpdaterKey{}, llmConfigUpdater(func(_ context.Context, cfg *engine.LLMConfig) error {
		require.Equal(t, "openai", cfg.DefaultProvider)
		require.Equal(t, "qwen-new", cfg.DefaultModel)
		return nil
	}))
	_, err = (&shellCallHandler{}).saveModelDefault(ctx, current)
	require.NoError(t, err)
	saved, err := llmconfig.Load()
	require.NoError(t, err)
	require.Equal(t, "openai", saved.LLM.DefaultProvider)
	require.Equal(t, "qwen-new", saved.LLM.DefaultModel)
	require.Equal(t, "qwen-old", saved.LLM.Providers["openai"].Model)
}

func TestModelPickerCurrentOnlyAndCancellation(t *testing.T) {
	for _, outcome := range []string{"current only", "cancel error", "cancel state"} {
		t.Run(outcome, func(t *testing.T) {
			useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{DefaultProvider: "local", DefaultModel: "current"}})
			before, err := os.ReadFile(llmconfig.ConfigFile)
			require.NoError(t, err)
			var queries []string
			dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
				var query dagger.Request
				require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
				queries = append(queries, query.Query)
				body := `{"data":{"node":{"model":"current","provider":"local","withModel":{"model":"next","provider":"local"}}}}`
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}))
			require.NoError(t, err)
			t.Cleanup(func() { dag.Close() })
			s := &LLMSession{dag: dag, plumbingCtx: t.Context()}
			a := s.newAgent("test")
			a.llm = core.Ref[*core.LLM](core.NewQuery(dag), "seed")
			s.target = a
			h := &shellCallHandler{llmSession: s}
			ctx := context.WithValue(t.Context(), llmModelConfigKey{}, llmModelConfigReader(func(context.Context) (*engine.LLMConfig, error) {
				return &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"local": {Model: "next"}}}, nil
			}))
			ctx = context.WithValue(ctx, llmConfigUpdaterKey{}, llmConfigUpdater(func(context.Context, *engine.LLMConfig) error {
				t.Fatal("current-only selection or cancellation must not reload config")
				return nil
			}))
			previous := Frontend
			t.Cleanup(func() { Frontend = previous })
			Frontend = &idtui.FrontendMock{HandleFormFunc: func(_ context.Context, form *huh.Form) error {
				field := form.GetFocusedField()
				require.Equal(t, modelSelection{Provider: "local", Model: "current"}, field.GetValue())
				field.Focus()
				field.Update(tea.KeyMsg{Type: tea.KeyDown})
				require.Equal(t, modelSelection{Provider: "local", Model: "next"}, field.GetValue())
				if outcome == "current only" {
					return nil
				}
				form.Update(huh.NextField())
				confirm := form.GetFocusedField()
				confirm.Focus()
				confirm.Update(tea.KeyMsg{Type: tea.KeyLeft})
				require.Equal(t, true, confirm.GetValue())
				if outcome == "cancel error" {
					return huh.ErrUserAborted
				}
				form.State = huh.StateAborted
				return nil
			}}
			require.NoError(t, h.selectModelInteractive(ctx))
			after, err := os.ReadFile(llmconfig.ConfigFile)
			require.NoError(t, err)
			require.Equal(t, before, after)
			if outcome == "current only" {
				require.Equal(t, "next", a.model)
				require.Equal(t, "local", a.provider)
				require.Contains(t, strings.Join(queries, "\n"), `provider:"local"`)
			} else {
				require.NotContains(t, strings.Join(queries, "\n"), "withModel")
			}
		})
	}
}

func TestModelPickerCurrentSelectionAndDefaultOptIn(t *testing.T) {
	current := modelSelection{Provider: "openai-codex", Model: "gpt-5"}
	options := []huh.Option[modelSelection]{
		huh.NewOption("GPT-5 (openai)", modelSelection{Provider: "openai", Model: "gpt-5"}),
		huh.NewOption("GPT-5 (openai-codex)", current),
	}
	var selected modelSelection
	useDefault := true // Every opening starts with an explicit opt-in.
	form := newModelPickerForm(options, current, &selected, &useDefault)
	require.Equal(t, current, selected)
	require.Contains(t, form.GetFocusedField().View(), "GPT-5 (openai-codex) (current)")
	require.NotContains(t, form.GetFocusedField().View(), "GPT-5 (openai) (current)")
	require.False(t, useDefault)
	form.Update(huh.NextField())
	confirm, ok := form.GetFocusedField().(*idtui.ExplicitConfirm)
	require.True(t, ok)
	require.Contains(t, confirm.View(), "Use as default for new conversations")
	require.Contains(t, confirm.View(), "Environment and .env model settings take priority")
	confirm.Focus()
	confirm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.False(t, useDefault, "enter alone must not save a default")
	confirm.Update(tea.KeyMsg{Type: tea.KeyLeft})
	require.True(t, useDefault, "saving must require explicitly choosing Yes")
}

func TestModelOptionsKeepsCurrentCustomModel(t *testing.T) {
	current := modelSelection{Provider: "local", Model: "my-current-model"}
	options := modelOptions(&engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"local": {}}}, current)
	require.Len(t, options, 1)
	require.Equal(t, current, options[0].Value)
	require.Empty(t, modelOptions(&engine.LLMConfig{}, current), "do not restore an unconfigured provider")
}

func TestSaveModelDefault(t *testing.T) {
	for _, tc := range []struct {
		name, envModel, dotenv, warning string
		saved                           bool
		disabled                        bool
	}{
		{name: "saved provider", saved: true},
		{name: "environment provider without saved credentials"},
		{name: "inherited provider without local credentials"},
		{name: "environment model overrides saved default", envModel: "gpt-env", warning: "override"},
		{name: "dotenv model overrides saved default", dotenv: "OPENAI_MODEL=gpt-dotenv\n", warning: "override"},
		{name: "disabled saved provider stays disabled", saved: true, disabled: true, warning: "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLLMEnv(t)
			cfg := &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
				"anthropic": {Enabled: true, APIKey: "preserved-key", Model: "claude-preserved", ReasoningEffort: "high"},
			}}}
			if tc.saved {
				cfg.LLM.Providers["openai"] = llmconfig.Provider{Enabled: !tc.disabled, APIKey: "saved-openai-key", Model: "gpt-pinned"}
			}
			useTempLLMConfig(t, cfg)
			data, err := os.ReadFile(llmconfig.ConfigFile)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(llmconfig.ConfigFile, append(data, []byte("\n[other]\nvalue = 'preserved'\n")...), 0600))
			if tc.name == "environment provider without saved credentials" || tc.disabled {
				t.Setenv("OPENAI_API_KEY", "env-only-key")
			}
			if tc.envModel != "" {
				t.Setenv("OPENAI_MODEL", tc.envModel)
			}
			if tc.dotenv != "" {
				require.NoError(t, os.WriteFile(".env", []byte(tc.dotenv), 0600))
			}
			reloads := 0
			ctx := context.WithValue(t.Context(), llmConfigUpdaterKey{}, llmConfigUpdater(func(context.Context, *engine.LLMConfig) error {
				reloads++
				return nil
			}))
			h := &shellCallHandler{}
			notice, err := h.saveModelDefault(ctx, modelSelection{Provider: "openai", Model: "gpt-selected"})
			require.NoError(t, err)
			require.Equal(t, 1, reloads)
			loaded, err := llmconfig.Load()
			require.NoError(t, err)
			require.Equal(t, "openai", loaded.LLM.DefaultProvider)
			require.Equal(t, "gpt-selected", loaded.LLM.DefaultModel)
			require.Equal(t, cfg.LLM.Providers, loaded.LLM.Providers, "never copy or re-enable credentials")
			data, err = os.ReadFile(llmconfig.ConfigFile)
			require.NoError(t, err)
			require.Contains(t, string(data), "[other]")
			require.Contains(t, string(data), "preserved")
			require.NotContains(t, string(data), "env-only-key")
			require.Contains(t, notice, "Saved default for new conversations: gpt-selected (openai).")
			if tc.warning != "" {
				require.Contains(t, notice, tc.warning)
			} else {
				require.NotContains(t, notice, "override")
				require.NotContains(t, notice, "disabled")
			}
		})
	}
}

func TestSaveModelDefaultFilelessAndReloadFailure(t *testing.T) {
	clearLLMEnv(t)
	useTempLLMConfig(t, nil)
	wantErr := errors.New("reload unavailable")
	ctx := context.WithValue(t.Context(), llmConfigUpdaterKey{}, llmConfigUpdater(func(_ context.Context, cfg *engine.LLMConfig) error {
		require.Equal(t, "openai-codex", cfg.DefaultProvider)
		require.Equal(t, "gpt-selected", cfg.DefaultModel)
		require.Empty(t, cfg.Providers, "parent credentials must not be copied")
		return wantErr
	}))
	_, err := (&shellCallHandler{}).saveModelDefault(ctx, modelSelection{Provider: "openai-codex", Model: "gpt-selected"})
	require.ErrorIs(t, err, wantErr)
	require.ErrorContains(t, err, "saved")
	cfg, err := llmconfig.Load()
	require.NoError(t, err)
	require.Equal(t, "openai-codex", cfg.LLM.DefaultProvider)
	require.Equal(t, "gpt-selected", cfg.LLM.DefaultModel)
	require.Empty(t, cfg.LLM.Providers)
}

func TestAvailableModelOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    *llmconfig.Config
		env       map[string]string
		dotenv    string
		providers []string
		models    []modelSelection
		absent    []modelSelection
	}{
		{name: "no configuration"},
		{
			name:      "environment only codex",
			env:       map[string]string{"OPENAI_CODEX_AUTH_TOKEN": "test-token"},
			providers: []string{"openai-codex"},
		},
		{
			name: "same ID different providers",
			config: &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
				"openai":       {Enabled: true, APIKey: "test-key", Model: "gpt-5"},
				"openai-codex": {Enabled: true, AuthType: "oauth", AuthToken: "test-token", Model: "gpt-5"},
				"anthropic":    {Enabled: false, APIKey: "disabled-key"},
			}}},
			providers: []string{"openai", "openai-codex"},
			models:    []modelSelection{{"openai", "gpt-5"}, {"openai-codex", "gpt-5"}},
		},
		{
			name: "custom default and small models",
			config: &llmconfig.Config{LLM: llmconfig.LLMConfig{
				DefaultProvider: "local", DefaultModel: "org/default-model",
				Providers: map[string]llmconfig.Provider{
					"local": {Enabled: true, BaseURL: "http://localhost:8080", Model: "org/pinned-model", SmallModel: "org/small-model"},
				},
			}},
			providers: []string{"local"},
			models:    []modelSelection{{"local", "org/default-model"}, {"local", "org/pinned-model"}, {"local", "org/small-model"}},
		},
		{
			name: "dotenv overrides environment and file",
			config: &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
				"local": {Enabled: true, BaseURL: "http://localhost:8080", Model: "file-model"},
			}}},
			env:       map[string]string{"LOCAL_MODEL": "env-model"},
			dotenv:    "LOCAL_MODEL=org/dotenv-model\nOPENROUTER_API_KEY=test-key\nOPENROUTER_MODEL=vendor/custom-model\n",
			providers: []string{"local", "openrouter"},
			models:    []modelSelection{{"local", "org/dotenv-model"}, {"openrouter", "vendor/custom-model"}},
			absent:    []modelSelection{{"local", "file-model"}, {"local", "env-model"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLLMEnv(t)
			for _, name := range []string{"OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENROUTER_MODEL", "OPENROUTER_SMALL_MODEL", "OPENROUTER_REASONING_EFFORT"} {
				t.Setenv(name, "")
			}
			useTempLLMConfig(t, tc.config)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			if tc.dotenv != "" {
				require.NoError(t, os.WriteFile(".env", []byte(tc.dotenv), 0600))
			}
			options, err := availableModelOptions(t.Context(), modelSelection{})
			require.NoError(t, err)
			seen := map[modelSelection]bool{}
			providers := map[string]bool{}
			for _, option := range options {
				require.False(t, seen[option.Value], "duplicate provider/model option: %v", option.Value)
				seen[option.Value] = true
				providers[option.Value.Provider] = true
			}
			require.Len(t, providers, len(tc.providers))
			for _, provider := range tc.providers {
				require.True(t, providers[provider], "missing provider %s", provider)
			}
			for _, model := range tc.models {
				require.True(t, seen[model], "missing model %v", model)
			}
			for _, model := range tc.absent {
				require.False(t, seen[model], "unexpected model %v", model)
			}
		})
	}
}

func TestAvailableModelOptionsEffectiveConfig(t *testing.T) {
	// The main client can supply credentials to a nested agent with no local
	// configuration. The engine returns only provider names and model metadata.
	clearLLMEnv(t)
	useTempLLMConfig(t, nil)
	cfg := &engine.LLMConfig{
		DefaultProvider: "local",
		DefaultModel:    "org/inherited-default",
		Providers: map[string]*engine.LLMProviderConfig{
			"openai-codex": {},
			"local":        {Model: "org/inherited-model", SmallModel: "org/inherited-small"},
		},
	}
	calls := 0
	read := llmModelConfigReader(func(ctx context.Context) (*engine.LLMConfig, error) {
		calls++
		require.Equal(t, t.Context().Done(), ctx.Done())
		return cfg, nil
	})
	ctx := context.WithValue(t.Context(), llmModelConfigKey{}, read)
	options, err := availableModelOptions(ctx, modelSelection{})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	seen := map[modelSelection]bool{}
	for _, option := range options {
		seen[option.Value] = true
		require.Contains(t, []string{"local", "openai-codex"}, option.Value.Provider)
	}
	for _, model := range []string{"org/inherited-default", "org/inherited-model", "org/inherited-small"} {
		require.True(t, seen[modelSelection{Provider: "local", Model: model}])
	}
	for _, model := range llmconfig.ModelsForProvider("openai-codex") {
		require.True(t, seen[modelSelection{Provider: "openai-codex", Model: model.ID}])
	}

	// A live reader is authoritative. Do not reintroduce local providers after
	// the engine reports none (e.g. logout), or conceal a failed config read.
	t.Setenv("OPENAI_API_KEY", "local-key")
	cfg = nil
	options, err = availableModelOptions(ctx, modelSelection{})
	require.NoError(t, err)
	require.Empty(t, options)
	wantErr := errors.New("engine config unavailable")
	ctx = context.WithValue(t.Context(), llmModelConfigKey{}, llmModelConfigReader(func(context.Context) (*engine.LLMConfig, error) {
		return nil, wantErr
	}))
	options, err = availableModelOptions(ctx, modelSelection{})
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, options)
}

func TestAvailableModelOptionsMalformedConfig(t *testing.T) {
	clearLLMEnv(t)
	useTempLLMConfig(t, nil)
	t.Setenv("OPENAI_DISABLE_STREAMING", "not-a-bool")
	_, err := availableModelOptions(t.Context(), modelSelection{})
	require.ErrorIs(t, err, llmconfig.ErrMalformed)
}

func TestReasoningEffortOptions(t *testing.T) {
	values := func(opts []huh.Option[string]) []string {
		var vs []string
		for _, o := range opts {
			vs = append(vs, o.Value)
		}
		return vs
	}
	catalogModel := func(match func(llmconfig.ModelInfo) bool) string {
		t.Helper()
		for _, provider := range llmconfig.ProviderEntries() {
			for _, model := range llmconfig.ModelsForProvider(provider.ConfigKey) {
				if match(model) {
					return model.ID
				}
			}
		}
		t.Fatal("catwalk catalog has no model matching the test condition")
		return ""
	}

	// Catalog model marked non-reasoning: only "none", no invented levels.
	nonReasoning := catalogModel(func(model llmconfig.ModelInfo) bool {
		return !model.CanReason
	})
	require.Equal(t, []string{"none"}, values(reasoningEffortOptions(nonReasoning)))

	// Unknown model: conventional fallback.
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions("my-local-model")))

	// Catalog model that can reason but declares no explicit levels
	// gets the conventional fallback.
	withoutLevels := catalogModel(func(model llmconfig.ModelInfo) bool {
		return model.CanReason && len(model.ReasoningLevels) == 0
	})
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions(withoutLevels)))
}
