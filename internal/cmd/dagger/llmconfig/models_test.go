package llmconfig

import "testing"

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
