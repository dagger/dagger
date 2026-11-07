package llmconfig

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/stretchr/testify/require"
)

func TestCopilotCredentialSnapshot(t *testing.T) {
	for _, env := range []bool{false, true} {
		t.Run(map[bool]string{false: "stored", true: "environment"}[env], func(t *testing.T) {
			isolateCopilotEnv(t)
			s := newFakeCopilotServer(t)
			s.api = "https://first.example"
			p := expiredOAuthProvider("stored-github")
			p.BaseURL = "https://stale.example"
			useTempConfig(t, fileConfig("github-copilot", "gpt-4.1", map[string]Provider{"github-copilot": p}))
			before, err := os.ReadFile(ConfigFile)
			require.NoError(t, err)
			grant := "stored-github"
			if env {
				grant = "env-github"
				t.Setenv(copilotTokenEnv, grant)
			}
			read := func(ctxToken string) engine.CopilotCredential {
				ctx := secretprovider.ContextWithRejectedSecretValue(t.Context(), ctxToken)
				data, err := ResolveSecret(ctx, strings.TrimPrefix(engine.CopilotCredentialURI, "llmconfig://"))
				require.NoError(t, err)
				var snapshot engine.CopilotCredential
				require.NoError(t, json.Unmarshal(data, &snapshot))
				require.Equal(t, time.Unix(s.expiresAt, 0).UTC(), snapshot.ExpiresAt)
				return snapshot
			}
			first := read("")
			require.Equal(t, "https://first.example", first.BaseURL)
			require.Equal(t, read(""), first)
			require.Equal(t, []string{"token " + grant}, s.tokens())
			s.mu.Lock()
			s.api = "https://second.example"
			s.mu.Unlock()
			second := read(copilotFingerprint(first.Token))
			require.NotEqual(t, first.Token, second.Token)
			require.Equal(t, "https://second.example", second.BaseURL)
			require.Equal(t, []string{"token " + grant, "token " + grant}, s.tokens())
			require.Equal(t, second, read(""))
			if env {
				after, err := os.ReadFile(ConfigFile)
				require.NoError(t, err)
				require.Equal(t, string(before), string(after))
			} else {
				cfg, err := Load()
				require.NoError(t, err)
				persisted := cfg.LLM.Providers["github-copilot"]
				require.Equal(t, second.Token, persisted.AuthToken)
				require.Equal(t, second.BaseURL, persisted.BaseURL)
			}
		})
	}
}
