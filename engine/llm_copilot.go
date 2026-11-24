package engine

import (
	"strings"
	"time"
)

// CopilotCredentialURI resolves one exchange's bearer, expiry and API origin
// atomically through the client's existing secret resolver. It is a companion
// to llmconfig://github-copilot/auth_token, not client metadata.
const CopilotCredentialURI = "llmconfig://github-copilot/credential" //nolint:gosec // resolver address, not a credential

// CopilotCredential is a single token exchange's result, served as a secret.
type CopilotCredential struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	BaseURL   string    `json:"base_url"`
}

// CopilotHeaders identifies Dagger while using Copilot's allow-listed chat
// integration. Shared by the CLI's token exchange and the engine's LLM clients.
func CopilotHeaders() map[string]string {
	identity := "dagger/" + strings.TrimPrefix(Version, "v")
	return map[string]string{
		"Copilot-Integration-Id": "vscode-chat",
		"User-Agent":             identity,
		"Editor-Version":         identity,
	}
}
