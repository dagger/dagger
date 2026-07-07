package llmconfig

import "testing"

func TestCodexCatalog(t *testing.T) {
	models := ModelsForProvider("openai-codex")
	if len(models) == 0 {
		t.Fatal("expected openai-codex models, got none")
	}

	// Current Codex-with-ChatGPT models no longer contain "codex" in their IDs,
	// so the catalog must not be derived by filtering on that substring.
	byID := map[string]ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	for _, want := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"} {
		if _, ok := byID[want]; !ok {
			t.Errorf("expected Codex catalog to include %q", want)
		}
	}

	// The retired unversioned gpt-5-codex must not be offered.
	if _, ok := byID["gpt-5-codex"]; ok {
		t.Error("retired gpt-5-codex should not be in the Codex catalog")
	}

	if got := DefaultModelForProvider("openai-codex"); got != "gpt-5.5" {
		t.Errorf("default Codex model = %q, want gpt-5.5", got)
	}
}
