package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/openai/openai-go/v3/option"
)

const copilotBaseURL = "https://api.githubcopilot.com"

func copilotCredentialReloader(resolve func(context.Context, string) (string, error)) credentialResolver {
	return func(ctx context.Context) (Credential, error) {
		value, err := resolve(ctx, engine.CopilotCredentialURI)
		if err != nil {
			return Credential{}, err
		}
		var snapshot engine.CopilotCredential
		if err := json.Unmarshal([]byte(value), &snapshot); err != nil {
			// Never include the credential payload in diagnostics.
			return Credential{}, fmt.Errorf("decode Copilot credential snapshot")
		}
		if snapshot.Token == "" || snapshot.BaseURL == "" || snapshot.ExpiresAt.IsZero() {
			return Credential{}, fmt.Errorf("incomplete Copilot credential snapshot")
		}
		if _, err := copilotAPIOrigin(snapshot.BaseURL); err != nil {
			return Credential{}, err
		}
		return Credential{Token: snapshot.Token, ExpiresAt: snapshot.ExpiresAt, BaseURL: snapshot.BaseURL}, nil
	}
}

func copilotAPIOrigin(baseURL string) (*url.URL, error) {
	origin, err := url.Parse(baseURL)
	if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, fmt.Errorf("invalid Copilot API origin")
	}
	return origin, nil
}

func (r *LLMRouter) routeCopilotModel() *LLMEndpoint {
	cfg := r.provider(GitHubCopilot)
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = copilotBaseURL
	}
	return &LLMEndpoint{
		Provider:        GitHubCopilot,
		BaseURL:         baseURL,
		IsOAuth:         true,
		ReasoningEffort: cfg.ReasoningEffort,
	}
}

// usesCopilotResponses is independent of usesOpenAIResponses: a Copilot
// endpoint may have any host, including a Business/Enterprise API origin.
// GPT-5 and later (except GPT-5 mini) and Codex use Responses. This follows
// OpenCode's fallback for models without live supported_endpoints metadata:
// https://github.com/anomalyco/opencode/blob/dev/packages/core/src/plugin/provider/github-copilot.ts
func (endpoint *LLMEndpoint) usesCopilotResponses() bool {
	model := endpoint.Model
	if strings.Contains(model, "codex") || strings.HasPrefix(model, "grok-") || strings.HasPrefix(model, "mai-code-") {
		return true
	}
	if !strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "gpt-5-mini") {
		return false
	}
	major := strings.SplitN(strings.TrimPrefix(model, "gpt-"), ".", 2)[0]
	major = strings.SplitN(major, "-", 2)[0]
	version, err := strconv.Atoi(major)
	return err == nil && version >= 5
}

// copilotClientOptions supplies only Copilot's identity and bearer snapshot.
// AuthTokenSource still supersedes that snapshot on every HTTP request through
// credentialTransport, including when the SDK retries or the token rotates.
func (endpoint *LLMEndpoint) copilotClientOptions() []option.RequestOption {
	if endpoint.Provider != GitHubCopilot {
		return nil
	}
	opts := []option.RequestOption{option.WithAPIKey(endpoint.AuthToken)}
	for name, value := range engine.CopilotHeaders() {
		opts = append(opts, option.WithHeader(name, value))
	}
	return opts
}

func (endpoint *LLMEndpoint) copilotRequestOptions(history []*LLMMessage) []option.RequestOption {
	if endpoint.Provider != GitHubCopilot {
		return nil
	}
	initiator := "user"
	// GitHub documents that autonomous tool calls don't count as premium
	// requests, but doesn't document this header's billing contract:
	// https://docs.github.com/en/copilot/reference/copilot-billing/request-based-billing-legacy/copilot-requests
	// Follow Pi/OpenCode's user/agent convention, adapted to Dagger's content:
	// https://github.com/badlogic/pi-mono/blob/main/packages/ai/src/api/github-copilot-headers.ts
	if len(history) > 0 {
		// The agent loop puts tool results in user-role messages. Earlier tool
		// turns must not turn a new user prompt into an agent request.
		for _, block := range history[len(history)-1].Content {
			if block.Kind == LLMContentToolResult {
				initiator = "agent"
				break
			}
		}
	}
	return []option.RequestOption{option.WithHeader("X-Initiator", initiator)}
}
