package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnthropicOAuthUserAgent locks in the Claude Code identity the
// subscription OAuth client presents on the wire. Anthropic gates newly
// released models on the version in this header, so the built-in default must
// be what goes out when nothing is configured, and a configured override must
// replace it verbatim.
func TestAnthropicOAuthUserAgent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		want    string
	}{
		{
			name: "default",
			want: "claude-cli/" + defaultClaudeCodeVersion + " (external, cli)",
		},
		{
			name:    "override",
			version: "9.9.9",
			want:    "claude-cli/9.9.9 (external, cli)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Get("User-Agent"))
				mu.Unlock()
				// A 400 is not retried by the SDK, and the body is irrelevant:
				// only the request headers are under test.
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(srv.Close)

			endpoint := &LLMEndpoint{
				Provider:          Anthropic,
				IsOAuth:           true,
				AuthToken:         "token",
				BaseURL:           srv.URL,
				ClaudeCodeVersion: tc.version,
			}
			client := newAnthropicClient(endpoint)
			_, _ = client.client.Messages.New(context.Background(), anthropic.MessageNewParams{
				Model:     "claude-test",
				MaxTokens: 1,
				Messages: []anthropic.MessageParam{
					anthropic.NewUserMessage(anthropic.NewTextBlock("hi")),
				},
			})

			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []string{tc.want}, seen)
		})
	}
}

// TestLlmConfigClaudeCodeVersion covers the ANTHROPIC_CLAUDE_CODE_VERSION
// override: a bare X.Y.Z is accepted, and anything else is rejected at load
// time rather than silently falling back to the bundled default the user was
// trying to replace.
func TestLlmConfigClaudeCodeVersion(t *testing.T) {
	ctx := llmTestContext()

	t.Run("unset leaves the default in place", func(t *testing.T) {
		r := new(LLMRouter)
		_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{}))
		require.NoError(t, err)
		assert.Empty(t, r.AnthropicClaudeCodeVersion)
	})

	t.Run("bare X.Y.Z is accepted", func(t *testing.T) {
		r := new(LLMRouter)
		_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_CLAUDE_CODE_VERSION": "2.1.260",
		}))
		require.NoError(t, err)
		assert.Equal(t, "2.1.260", r.AnthropicClaudeCodeVersion)
	})

	for _, bad := range []string{"v2.1.260", "2.1", "2.1.260-beta", "latest"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			r := new(LLMRouter)
			_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
				"ANTHROPIC_CLAUDE_CODE_VERSION": bad,
			}))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ANTHROPIC_CLAUDE_CODE_VERSION")
		})
	}
}
