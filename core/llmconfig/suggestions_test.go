package llmconfig

import (
	"testing"
)

func TestSecretSuggestionsNoScheme(t *testing.T) {
	// Without a scheme, should suggest all schemes
	suggestions := SecretSuggestions("")
	if len(suggestions) != len(SecretSchemes) {
		t.Errorf("expected %d scheme suggestions, got %d", len(SecretSchemes), len(suggestions))
	}
	for i, s := range suggestions {
		if s != SecretSchemes[i] {
			t.Errorf("suggestion[%d] = %q, want %q", i, s, SecretSchemes[i])
		}
	}
}

func TestSecretSuggestionsPartialScheme(t *testing.T) {
	// Partial text without :// should still return all schemes (huh filters them)
	suggestions := SecretSuggestions("op")
	if len(suggestions) != len(SecretSchemes) {
		t.Errorf("expected %d scheme suggestions, got %d", len(SecretSchemes), len(suggestions))
	}
}

func TestSecretSuggestionsLiteralToken(t *testing.T) {
	// A literal token (no ://) should get scheme suggestions
	suggestions := SecretSuggestions("sk-ant-12345")
	if len(suggestions) != len(SecretSchemes) {
		t.Errorf("expected %d scheme suggestions for literal token, got %d", len(SecretSchemes), len(suggestions))
	}
}

func TestSecretSuggestionsNonOpScheme(t *testing.T) {
	// Non-op scheme should return nil (no further completions)
	suggestions := SecretSuggestions("env://MY_KEY")
	if suggestions != nil {
		t.Errorf("expected nil for env:// scheme, got %v", suggestions)
	}
}
