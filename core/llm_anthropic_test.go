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

	"github.com/dagger/dagger/engine"
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

// TestLlmConfigClaudeCodeVersion covers the claude_code_version override on
// an endpoint: a configured bare X.Y.Z reaches the Claude Code user-agent, and
// the router rejects anything else (see TestLLMRouterClaudeCodeVersion).
func TestLlmConfigClaudeCodeVersion(t *testing.T) {
	t.Run("unset leaves the default in place", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {AuthToken: "env://ANTHROPIC_AUTH_TOKEN"},
		})
		ep, err := r.Route("claude-x", "")
		require.NoError(t, err)
		assert.Empty(t, ep.ClaudeCodeVersion)
	})

	t.Run("bare X.Y.Z is accepted", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {AuthToken: "env://ANTHROPIC_AUTH_TOKEN", ClaudeCodeVersion: "2.1.260"},
		})
		ep, err := r.Route("claude-x", "")
		require.NoError(t, err)
		assert.Equal(t, "2.1.260", ep.ClaudeCodeVersion)
	})
}
