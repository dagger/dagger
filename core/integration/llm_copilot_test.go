package core

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

//go:embed testdata/copilot/server.py
var copilotFakeServer string

func copilotObservedHeaders(values map[string]string) http.Header {
	headers := http.Header{}
	for name, value := range values {
		headers.Set(name, value)
	}
	return headers
}

type copilotScenario struct {
	scenario string
	model    string
	env      bool
	stored   bool
	def      bool
}

func (tc copilotScenario) config() string {
	config := "[llm]\n"
	if tc.def && !tc.env {
		config += fmt.Sprintf("default_provider = 'github-copilot'\ndefault_model = %q\n", tc.model)
	}
	if tc.stored {
		githubToken := "github-" + tc.scenario
		if tc.env {
			githubToken = "github-unused-stored-login"
		}
		expires := time.Now().Add(30 * time.Minute).UnixMilli()
		if tc.scenario == "stored-expired" || tc.def {
			expires = time.Now().Add(-time.Hour).UnixMilli()
		}
		config += fmt.Sprintf(`[llm.providers.github-copilot]
enabled = true
auth_type = "oauth"
auth_token = "copilot-%s-0"
refresh_token = "%s"
token_expires_at = %d
base_url = "http://fake-copilot:8000"
model = %q
`, tc.scenario, githubToken, expires, tc.model)
	}
	return config
}

// TestCopilot exercises CLI assembly, real session secret RPCs, the memoized
// engine endpoint and both OpenAI clients. No public provider is reachable:
// unexpected outbound engine HTTP goes to the fake server's denying proxy.
// It is part of TestLLM, hence already included in test-split's test-llm shard.
func (LLMSuite) TestCopilot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fake := core.NewQuery(c).Container().From("python:3.13-alpine").
		WithNewFile("/server.py", copilotFakeServer).
		WithEnvVariable("TEST_INSTANCE", rand.Text()).
		WithDefaultArgs([]string{"python", "/server.py"}).
		WithExposedPort(8000).WithExposedPort(8001).WithExposedPort(8002).AsService()
	fake, err := fake.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = fake.Stop(ctx) })
	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *core.Container) *core.Container {
		return ctr.WithServiceBinding("fake-copilot", fake).
			WithEnvVariable("HTTP_PROXY", "http://fake-copilot:8002").
			WithEnvVariable("HTTPS_PROXY", "http://fake-copilot:8002").
			WithEnvVariable("NO_PROXY", "fake-copilot,localhost,127.0.0.1")
	}))
	devEngine, err = devEngine.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = devEngine.Stop(ctx) })

	for _, provider := range []string{"openai", "anthropic", "google"} {
		t.Run("saved-model-with-"+provider+"-key", func(ctx context.Context, t *testctx.T) {
			// Setup saves a per-provider model even if Copilot is not made
			// the default. Cross the CLI assembly boundary with that config.
			tc := copilotScenario{scenario: "priority-" + provider, model: "claude-sonnet-4.5", stored: true}
			config := tc.config() + fmt.Sprintf("\n[llm.providers.%s]\nenabled = true\napi_key = 'fake-key'\n", provider)
			cli := engineClientContainer(ctx, t, c, devEngine).
				WithEnvVariable("DAGGER_CONFIG", "/config.toml").
				WithNewFile("/config.toml", config).
				WithWorkdir("/work")
			// Route inspection is pure: no prompt or credential resolution,
			// and the engine's denying proxy also prevents live API calls.
			query := fmt.Sprintf(`{
llm { provider model }
explicit: llm(provider: %q) { provider model }
copilot: llm(provider: "github-copilot") { provider model }
}`, provider)
			out, err := cli.WithExec([]string{"env", "-u", "DAGGER_SESSION_PORT", "-u", "DAGGER_SESSION_TOKEN", "dagger", "query"},
				core.ContainerWithExecOpts{Stdin: query}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, provider, gjson.Get(out, "llm.provider").String())
			require.Equal(t, provider, gjson.Get(out, "explicit.provider").String())
			model := gjson.Get(out, "explicit.model").String()
			require.NotEmpty(t, model)
			require.Equal(t, model, gjson.Get(out, "llm.model").String(), "use the API-key provider's own default model")
			require.Equal(t, "github-copilot", gjson.Get(out, "copilot.provider").String())
			require.Equal(t, tc.model, gjson.Get(out, "copilot.model").String())
		})
	}

	for _, tc := range []copilotScenario{
		{scenario: "stored-valid", model: "gpt-4.1", stored: true},
		{scenario: "stored-expired", model: "gpt-4.1", stored: true},
		{scenario: "reject-changed", model: "gpt-4.1", stored: true},
		{scenario: "reject-same", model: "gpt-4.1", stored: true},
		{scenario: "reject-responses", model: "gpt-5.3-codex", stored: true},
		{scenario: "env-only", model: "gpt-4.1", env: true},
		{scenario: "env-over-file", model: "gpt-4.1", env: true, stored: true},
		{scenario: "default-claude", model: "claude-sonnet-4.5", stored: true, def: true},
		{scenario: "default-gpt", model: "gpt-4.1", stored: true, def: true},
		{scenario: "default-gemini", model: "gemini-2.5-pro", stored: true, def: true},
		{scenario: "env-default", model: "claude-sonnet-4.5", env: true, def: true},
		{scenario: "env-default-gpt", model: "gpt-4.1", env: true, def: true},
		{scenario: "env-default-gemini", model: "gemini-2.5-pro", env: true, def: true},
	} {
		t.Run(tc.scenario, func(ctx context.Context, t *testctx.T) {
			config := tc.config()
			cli := engineClientContainer(ctx, t, c, devEngine).
				WithServiceBinding("fake-copilot", fake).
				WithEnvVariable("DAGGER_CONFIG", "/config.toml").
				WithEnvVariable("_DAGGER_TEST_COPILOT_TOKEN_URL", "http://fake-copilot:8002/exchange").
				WithNewFile("/config.toml", config).
				WithWorkdir("/work")
			if tc.env {
				cli = cli.WithEnvVariable("COPILOT_GITHUB_TOKEN", "github-"+tc.scenario)
				if tc.def {
					cli = cli.WithEnvVariable("COPILOT_MODEL", tc.model)
				}
			}
			selection := fmt.Sprintf(`llm(provider: "github-copilot", model: %q)`, tc.model)
			if tc.def {
				selection = "llm"
			}
			reject := strings.HasPrefix(tc.scenario, "reject-")
			turn := `withPrompt(prompt: "first") { step { lastReply } }`
			if reject {
				// All three prompts share one session and one conversation; the
				// second forces refresh, the third proves the new origin sticks.
				turn = `withPrompt(prompt: "first") { step {
lastReply withPrompt(prompt: "second") { step {
lastReply withPrompt(prompt: "third") { step { lastReply } }
} } } }`
			}
			query := fmt.Sprintf(`{ %s { provider model %s } }`, selection, turn)
			// Unset at exec time: the outer engine injects these after the
			// container's environment is assembled (withoutOuterSession).
			ran := cli.WithExec([]string{"env", "-u", "DAGGER_SESSION_PORT", "-u", "DAGGER_SESSION_TOKEN", "dagger", "api", "with-session", "sh", "-ec", `
wget -qO /status.json --header "Authorization: Basic $(printf '%s:' "$DAGGER_SESSION_TOKEN" | base64 | tr -d '\n')" "http://127.0.0.1:$DAGGER_SESSION_PORT/llm-config"
exec dagger query
`}, core.ContainerWithExecOpts{Stdin: query})
			out, err := ran.Stdout(ctx)
			require.NoError(t, err)
			status, err := ran.File("/status.json").Contents(ctx)
			require.NoError(t, err)
			require.True(t, gjson.Get(status, "configured").Bool(), "Copilot-only session must count as configured")
			require.True(t, gjson.Get(status, "models.providers.github-copilot").Exists())
			require.Equal(t, "github-copilot", gjson.Get(out, "llm.provider").String())
			require.Equal(t, tc.model, gjson.Get(out, "llm.model").String())
			generation := 0
			if tc.env || tc.def || tc.scenario == "stored-expired" || reject {
				generation = 1
			}
			replyPath := "llm.withPrompt.step.lastReply"
			if reject {
				require.Equal(t, "reply-"+tc.scenario+"-0", gjson.Get(out, replyPath).String())
				replyPath = "llm.withPrompt.step.withPrompt.step.lastReply"
				require.Equal(t, "reply-"+tc.scenario+"-1", gjson.Get(out, replyPath).String())
				replyPath = "llm.withPrompt.step.withPrompt.step.withPrompt.step.lastReply"
			}
			require.Equal(t, fmt.Sprintf("reply-%s-%d", tc.scenario, generation), gjson.Get(out, replyPath).String())
			state, err := ran.WithExec([]string{"wget", "-qO-", "http://fake-copilot:8002/state?scenario=" + tc.scenario}).Stdout(ctx)
			require.NoError(t, err)
			var seen struct {
				Exchanges []map[string]string `json:"exchanges"`
				Requests  []struct {
					Port    int               `json:"port"`
					Path    string            `json:"path"`
					Headers map[string]string `json:"headers"`
					Model   string            `json:"model"`
					Status  int               `json:"status"`
				} `json:"requests"`
				Blocked []string `json:"blocked"`
			}
			require.NoError(t, json.Unmarshal([]byte(state), &seen))
			require.Empty(t, seen.Blocked, "unexpected external provider request")
			require.Len(t, seen.Exchanges, generation)
			for _, headers := range seen.Exchanges {
				require.Equal(t, "token github-"+tc.scenario, copilotObservedHeaders(headers).Get("Authorization"))
			}
			wantRequests := 1
			if reject {
				wantRequests = 4 // first, rejected second, retried second, third
			}
			require.Len(t, seen.Requests, wantRequests)
			for i, req := range seen.Requests {
				port, tokenGeneration := 8001, generation
				if tc.scenario == "stored-valid" || tc.scenario == "reject-same" || (reject && i < 2) {
					port = 8000
				}
				if reject && i < 2 {
					tokenGeneration = 0
				}
				require.Equal(t, port, req.Port, "request %d: token reached wrong origin", i)
				headers := copilotObservedHeaders(req.Headers)
				require.Equal(t, fmt.Sprintf("Bearer copilot-%s-%d", tc.scenario, tokenGeneration), headers.Get("Authorization"))
				for name, value := range engine.CopilotHeaders() {
					require.Equal(t, value, headers.Get(name))
					for _, exchange := range seen.Exchanges {
						require.Equal(t, value, copilotObservedHeaders(exchange).Get(name))
					}
				}
				require.Equal(t, "user", headers.Get("X-Initiator"))
				require.Equal(t, tc.model, req.Model)
				path := "/chat/completions"
				if tc.scenario == "reject-responses" {
					path = "/responses"
				}
				require.Equal(t, path, req.Path)
				status := http.StatusOK
				if reject && i == 1 {
					status = http.StatusUnauthorized
				}
				require.Equal(t, status, req.Status)
			}
			persisted, err := ran.File("/config.toml").Contents(ctx)
			require.NoError(t, err)
			if tc.env {
				require.Equal(t, config, persisted, "env exchange must not overwrite a saved login")
			} else if generation == 1 {
				port := 8001
				if tc.scenario == "reject-same" {
					port = 8000
				}
				require.Contains(t, persisted, fmt.Sprintf(`base_url = "http://fake-copilot:%d"`, port))
				require.Contains(t, persisted, fmt.Sprintf(`auth_token = "copilot-%s-1"`, tc.scenario))
			}
		})
	}
}
