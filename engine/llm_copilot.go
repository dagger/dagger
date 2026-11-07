package engine

import "strings"

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
