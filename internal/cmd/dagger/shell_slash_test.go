package daggercmd

import (
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
)

// newSlashTestHandler builds a handler with the builtin commands registered but
// without touching an engine, so the prompt-mode slash-command routing and
// completion can be exercised in a plain unit test.
func newSlashTestHandler(t *testing.T) *shellCallHandler {
	t.Helper()
	h := newShellCallHandler(nil, &idtui.FrontendMock{})
	require.NoError(t, h.registerCommands())
	return h
}

func TestPromptSlashCommandMapping(t *testing.T) {
	h := newSlashTestHandler(t)

	cases := []struct {
		line string
		want string
		ok   bool
	}{
		// bare builtins map to their "." equivalents
		{"/clear", ".clear", true},
		{"/compact", ".compact", true},
		{"/help", ".help", true},
		{"/setup", ".setup", true},
		{"/config", "", false},
		{"/show-config", "", false},
		{"/logout", ".logout", true},
		{"/add-key anthropic", "", false},
		{"/remove-key openai", "", false},
		{"/set-default openai-codex gpt-6-astra", "", false},
		{"/reset", "", false},
		// arguments are carried through verbatim
		{"/model claude-sonnet-4-5", ".model claude-sonnet-4-5", true},
		{"/effort high", ".effort high", true},
		{"/help\tcreate", ".help\tcreate", true},
		// Shell-only builtins are not agent slash commands.
		{"/cd ./sub", "", false},
		{"/shell", ".shell", true},
		{"/exit", ".exit", true},
		// non-commands are left for the LLM
		{"/", "", false},
		{"/ compact", "", false},
		{"/notacommand", "", false},
		{"just a normal prompt", "", false},
		{"what does /compact do?", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got, ok := h.slashCommand(tc.line)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPromptSlashCommandCompletion(t *testing.T) {
	h := newSlashTestHandler(t)
	h.mode = modePrompt

	labels := func(input string, cursor int) []string {
		res := h.AutoComplete(input, cursor)
		out := make([]string, 0, len(res.Items))
		for _, item := range res.Items {
			out = append(out, item.Label)
		}
		return out
	}

	// A bare "/" lists the visible builtins.
	all := labels("/", 1)
	require.NotEmpty(t, all)
	require.Contains(t, all, "/help")
	for _, command := range agentCommandHelp {
		require.Contains(t, all, "/"+command.name)
	}
	for _, name := range []string{"config", "show-config", "set-default", "reset", "add-key", "remove-key", "cd", "ls", "types", "printenv", "cloud-logout"} {
		require.NotContains(t, all, "/"+name)
	}

	// A prefix narrows the suggestions and keeps them sorted.
	co := labels("/co", 3)
	require.ElementsMatch(t, []string{"/compact", "/context"}, co)
	require.True(t, sortedStrings(co), "completions should be sorted: %v", co)

	// Hidden builtins (e.g. .cd) are not suggested.
	require.NotContains(t, labels("/cd", 3), "/cd")

	// The trigger only fires at the very start of the line, so a slash inside
	// ordinary prose is left alone.
	require.Empty(t, labels("tell me about /re", len("tell me about /re")))

	// Ordinary prompt words yield no completions (only "/" and "$" do).
	require.Empty(t, labels("hello", 5))
}

func TestAgentHelpIsCurated(t *testing.T) {
	h := newSlashTestHandler(t)
	h.mode = modePrompt
	help := h.MainHelp() // must not need module/schema discovery
	require.Contains(t, help, "EVERYDAY COMMANDS")
	require.Contains(t, help, "ADVANCED")
	for _, command := range agentCommandHelp {
		require.Contains(t, help, "/"+command.name)
		mapped, ok := h.slashCommand("/" + command.name)
		require.True(t, ok)
		require.Equal(t, "."+command.name, mapped)
	}
	for _, removed := range []string{"/config", "/show-config", "/set-default", "/reset", "/printenv", "Available Functions"} {
		require.NotContains(t, help, removed)
	}
	for _, name := range []string{"model", "/model", ".model"} {
		details, err := h.promptHelp([]string{name})
		require.NoError(t, err)
		require.Contains(t, details, "Usage: /model [model]")
	}
	_, err := h.promptHelp([]string{"printenv"})
	require.ErrorContains(t, err, "unknown agent command")
}

func TestProviderLogoutDoesNotShadowCloudLogout(t *testing.T) {
	h := newSlashTestHandler(t)
	seen := map[string]bool{}
	for _, command := range h.builtins {
		require.False(t, seen[command.Name()], "duplicate builtin %s", command.Name())
		seen[command.Name()] = true
	}
	logout, err := h.BuiltinCommand(".logout")
	require.NoError(t, err)
	require.NotNil(t, logout)
	require.Equal(t, "llm", logout.GroupID)
	require.False(t, logout.Hidden)
	cloud, err := h.BuiltinCommand(".cloud-logout")
	require.NoError(t, err)
	require.NotNil(t, cloud)
	require.True(t, cloud.Hidden)
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
