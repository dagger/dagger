package llmconfig

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/catwalk/v3/pkg/embedded"
	"github.com/stretchr/testify/require"
)

func TestProviderDefaults(t *testing.T) {
	for provider, want := range map[string]string{
		"anthropic":      "claude-sonnet-5-5",
		"openai":         "gpt-6.2-sol",
		"openai-codex":   "gpt-6.2-sol",
		"google":         "gemini-3.8-flash",
		"github-copilot": "claude-sonnet-5.5",
		"openrouter":     "anthropic/claude-sonnet-5.5",
	} {
		t.Run(provider, func(t *testing.T) {
			require.Equal(t, want, DefaultModelForProvider(provider))
			model, ok := ModelByID(provider, want)
			require.True(t, ok)
			require.True(t, model.Default)
			var defaults int
			for _, model := range ModelsForProvider(provider) {
				if model.Default {
					defaults++
				}
			}
			require.Equal(t, 1, defaults)
		})
	}
	require.Empty(t, DefaultModelForProvider("local"))
	require.Empty(t, DefaultModelForProvider("unknown"))
}

func TestCodexCatalog(t *testing.T) {
	models := ModelsForProvider("openai-codex")
	if len(models) == 0 {
		t.Fatal("expected openai-codex models, got none")
	}

	// The Codex option is sourced from catwalk's OpenAI catalog. Current
	// Codex-with-ChatGPT models no longer contain "codex" in their IDs, so the
	// catalog must include them (it is not filtered on that substring), and each
	// carries the reasoning metadata catwalk provides.
	byID := map[string]ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	for _, want := range []string{"gpt-6.2-sol", "gpt-6-luna", "gpt-5.4-mini"} {
		m, ok := byID[want]
		if !ok {
			t.Errorf("expected Codex catalog to include %q", want)
			continue
		}
		if !m.CanReason || len(m.ReasoningLevels) == 0 {
			t.Errorf("%q should carry catwalk reasoning metadata, got %+v", want, m)
		}
	}

	// The default must be one of the catalog's models.
	if def := DefaultModelForProvider("openai-codex"); byID[def].ID == "" {
		t.Errorf("default Codex model %q not in catalog", def)
	}
}

func TestReasoningEffortLevels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model ModelInfo
		want  []catwalk.EffortLevel
	}{
		{
			name: "non-reasoning",
			want: catwalk.NewEffortLevels("none"),
		},
		{
			name:  "budget or unknown model uses conventional levels",
			model: ModelInfo{CanReason: true},
			want:  catwalk.NewEffortLevels("none", "low", "medium", "high"),
		},
		{
			name:  "always-thinking budget model cannot disable thinking",
			model: ModelInfo{CanReason: true, AlwaysReason: true},
			want:  catwalk.NewEffortLevels("low", "medium", "high"),
		},
		{
			name: "display names and wire values remain separate",
			model: ModelInfo{CanReason: true, ReasoningLevels: []catwalk.EffortLevel{
				{Value: "none", Display: "Disabled"},
				{Value: "xhigh", Display: "Extra High"},
				{Value: "custom"},
				{},
			}},
			want: []catwalk.EffortLevel{
				{Value: "none", Display: "Disabled"},
				{Value: "xhigh", Display: "Extra High"},
				{Value: "custom", Display: "custom"},
			},
		},
		{
			name:  "always-thinking ignores conflicting none level",
			model: ModelInfo{CanReason: true, AlwaysReason: true, ReasoningLevels: catwalk.NewEffortLevels("none", "high")},
			want:  catwalk.NewEffortLevels("high"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.model.ReasoningEffortLevels()
			require.Equal(t, tc.want, got)
			got[0].Display = "changed"
			require.Equal(t, tc.want, tc.model.ReasoningEffortLevels(), "must not modify catalog entries")
		})
	}
}

func TestSetupReasoningEffortOptions(t *testing.T) {
	m := ModelInfo{
		CanReason: true,
		ReasoningLevels: []catwalk.EffortLevel{
			{Value: "none", Display: "None"},
			{Value: "xhigh", Display: "Extra High"},
		},
		DefaultReasoningEffort: "xhigh",
	}
	options := setupReasoningEffortOptions(m)
	require.Len(t, options, 2)
	require.Equal(t, "Off", options[0].Key)
	require.Empty(t, options[0].Value, "preserve setup's saved off value")
	require.Equal(t, "Extra High (model default)", options[1].Key)
	require.Equal(t, "xhigh", options[1].Value)

	m.AlwaysReason = true
	options = setupReasoningEffortOptions(m)
	require.Len(t, options, 2)
	require.Equal(t, "Provider default", options[0].Key)
	require.Empty(t, options[0].Value, "allow resetting config without promising to disable thinking")
	require.Equal(t, "xhigh", options[1].Value)

	m = ModelInfo{CanReason: true}
	options = setupReasoningEffortOptions(m)
	require.Len(t, options, 1, "setup must not persist invented effort levels for budget models")
	require.Empty(t, options[0].Value)

	gemini, ok := ModelByID("google", "gemini-2.5-flash")
	require.True(t, ok)
	require.Empty(t, gemini.ReasoningLevels)
	require.Len(t, setupReasoningEffortOptions(gemini), 1, "setup persists only catalog-declared native levels")
}

func TestCopilotCatalog(t *testing.T) {
	var copilot catwalk.Provider
	for _, p := range embedded.GetAll() {
		if p.ID == catwalk.InferenceProviderCopilot {
			copilot = p
			break
		}
	}
	models := ModelsForProvider("github-copilot")
	require.Len(t, models, len(copilot.Models))
	for i, m := range models {
		require.Equal(t, copilot.Models[i].ID, m.ID)
		require.Equal(t, copilot.Models[i].Name, m.Label)
		require.Equal(t, copilot.Models[i].Reasoning.EffortLevels, m.ReasoningLevels)
		require.Equal(t, copilot.Models[i].Reasoning.Thinking == catwalk.ThinkingAlways, m.AlwaysReason)
	}
	require.Equal(t, "claude-sonnet-5.5", DefaultModelForProvider("github-copilot"))
	require.Contains(t, ProviderEntries(), ProviderEntry{"GitHub Copilot (subscription)", "github-copilot", "github-copilot", true})
}
