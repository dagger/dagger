package core

import (
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/openai/openai-go/v3/option"
)

const copilotBaseURL = "https://api.githubcopilot.com"

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
