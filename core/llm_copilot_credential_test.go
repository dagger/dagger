package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/stretchr/testify/require"
)

func TestCopilotCredentialOriginRotation(t *testing.T) {
	for _, model := range []string{"gpt-4.1", "gpt-5.3-codex"} {
		for _, sameOrigin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/same=%t", model, sameOrigin), func(t *testing.T) {
				var mu sync.Mutex
				var observed []string
				handler := func(origin string) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						token := r.Header.Get("Authorization")
						observed = append(observed, origin+" "+token)
						if len(observed) == 2 {
							w.WriteHeader(http.StatusUnauthorized)
							_, _ = io.WriteString(w, `{"error":{"message":"rejected"}}`)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						if r.URL.Path == "/responses" {
							_, _ = io.WriteString(w, "event: response.completed\n"+`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp","object":"response","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}]}}`+"\n\n")
						} else {
							_, _ = io.WriteString(w, `data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
						}
					})
				}
				old := httptest.NewServer(handler("old"))
				t.Cleanup(old.Close)
				next := httptest.NewServer(handler("new"))
				t.Cleanup(next.Close)
				if sameOrigin {
					next = old
				}
				rotated := false
				reload := copilotCredentialReloader(func(ctx context.Context, uri string) (string, error) {
					require.Equal(t, engine.CopilotCredentialURI, uri)
					if rejected := secretprovider.RejectedSecretValue(ctx); rejected != "" {
						require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte("first"))), rejected)
						rotated = true
					}
					snapshot := engine.CopilotCredential{Token: "first", BaseURL: old.URL, ExpiresAt: time.Now().Add(time.Hour)}
					if rotated {
						snapshot.Token, snapshot.BaseURL = "second", next.URL
					}
					data, err := json.Marshal(snapshot)
					return string(data), err
				})
				// Build once with a stale routing URL; every attempt must instead
				// use its resolved credential's origin, including after a 401.
				ep := &LLMEndpoint{Provider: GitHubCopilot, Model: model, BaseURL: "http://stale.invalid", AuthTokenSource: newCredentialSource(reload)}
				client, err := ep.newClient()
				require.NoError(t, err)
				history := []*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}}}
				for i := range 4 {
					response, err := client.SendQuery(t.Context(), history, nil, &LLMCallOpts{})
					if i == 1 {
						require.Error(t, err)
						require.True(t, isAuthFailure(err))
					} else {
						require.NoError(t, err)
						require.Equal(t, "done", response.Content[0].Text)
					}
				}
				newOrigin := "new"
				if sameOrigin {
					newOrigin = "old"
				}
				mu.Lock()
				defer mu.Unlock()
				require.Equal(t, []string{"old Bearer first", "old Bearer first", newOrigin + " Bearer second", newOrigin + " Bearer second"}, observed)
			})
		}
	}
}

func TestCopilotCredentialRejectsMalformedSnapshot(t *testing.T) {
	for _, value := range []string{
		`{"token":"secret"`,
		`{"token":"secret","base_url":"http://example.com"}`,
		`{"token":"secret","expires_at":"2030-01-01T00:00:00Z","base_url":"http://user:pass@example.com"}`,
		`{"token":"secret","expires_at":"2030-01-01T00:00:00Z","base_url":"http://example.com/path"}`,
	} {
		_, err := copilotCredentialReloader(func(context.Context, string) (string, error) { return value, nil })(t.Context())
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}
