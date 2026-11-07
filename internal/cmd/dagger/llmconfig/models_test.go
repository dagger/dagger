package llmconfig

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
	"github.com/stretchr/testify/require"
)

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
	for _, want := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"} {
		m, ok := byID[want]
		if !ok {
			t.Errorf("expected Codex catalog to include %q", want)
			continue
		}
		if !m.CanReason || len(m.ReasoningLevels) == 0 {
			t.Errorf("%q should carry catwalk reasoning metadata, got %+v", want, m)
		}
	}

	// The default follows catwalk and must be one of the catalog's models.
	if def := DefaultModelForProvider("openai-codex"); byID[def].ID == "" {
		t.Errorf("default Codex model %q not in catalog", def)
	}
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
		require.Equal(t, copilot.Models[i].ReasoningLevels, m.ReasoningLevels)
	}
	if len(copilot.Models) > 0 {
		require.Equal(t, copilot.DefaultLargeModelID, DefaultModelForProvider("github-copilot"))
	}
	require.Contains(t, ProviderEntries(), ProviderEntry{"GitHub Copilot (subscription)", "github-copilot", "github-copilot", true})
}
