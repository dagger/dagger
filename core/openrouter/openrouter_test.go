package openrouter

import "testing"

func TestDaggerToOpenRouter(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Anthropic models
		{"claude-sonnet-4-5", "anthropic/claude-sonnet-4.5"},
		{"claude-sonnet-4-6", "anthropic/claude-sonnet-4.6"},
		{"claude-opus-4-5", "anthropic/claude-opus-4.5"},
		{"claude-haiku-4-5", "anthropic/claude-haiku-4.5"},
		{"claude-sonnet-4-0", "anthropic/claude-sonnet-4"},
		{"claude-opus-4", "anthropic/claude-opus-4"},

		// OpenAI models
		{"gpt-5", "openai/gpt-5"},
		{"gpt-5-2", "openai/gpt-5.2"},
		{"gpt-5-2-chat", "openai/gpt-5.2-chat"},
		{"gpt-5-4-pro", "openai/gpt-5.4-pro"},
		{"gpt-5-1-codex-mini", "openai/gpt-5.1-codex-mini"},
		{"gpt-4o-audio-preview", "openai/gpt-4o-audio-preview"},
		{"o3-deep-research", "openai/o3-deep-research"},
		{"o4-mini-deep-research", "openai/o4-mini-deep-research"},

		// Google models
		{"gemini-2-5-flash", "google/gemini-2.5-flash"},
		{"gemini-3-pro-preview", "google/gemini-3-pro-preview"},
		{"gemini-3-1-flash-lite-preview", "google/gemini-3.1-flash-lite-preview"},
		{"gemini-2-5-flash-image", "google/gemini-2.5-flash-image"},

		// Already qualified — pass through
		{"anthropic/claude-sonnet-4.5", "anthropic/claude-sonnet-4.5"},
		{"openai/gpt-5", "openai/gpt-5"},

		// Unknown prefix — pass through
		{"llama-3-70b", "llama-3-70b"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := daggerToOpenRouter(tt.input)
			if got != tt.want {
				t.Errorf("daggerToOpenRouter(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
