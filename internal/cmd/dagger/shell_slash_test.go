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
		{"/resume", ".resume", true},
		{"/clear", ".clear", true},
		{"/compact", ".compact", true},
		{"/help", ".help", true},
		// arguments are carried through verbatim
		{"/resume abc123", ".resume abc123", true},
		{"/model claude-sonnet-4-5", ".model claude-sonnet-4-5", true},
		{"/help\tcreate", ".help\tcreate", true},
		// hidden builtins are still runnable as slash commands
		{"/cd ./sub", ".cd ./sub", true},
		// non-commands are left for the LLM
		{"/", "", false},
		{"/ resume", "", false},
		{"/notacommand", "", false},
		{"just a normal prompt", "", false},
		{"what does /resume do?", "", false},
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
	require.Contains(t, all, "/resume")

	// A prefix narrows the suggestions and keeps them sorted.
	re := labels("/re", 3)
	require.Contains(t, re, "/resume")
	require.Contains(t, re, "/refresh")
	require.True(t, sortedStrings(re), "completions should be sorted: %v", re)
	for _, l := range re {
		require.Contains(t, l, "/re")
	}

	// Hidden builtins (e.g. .cd) are not suggested.
	require.NotContains(t, labels("/cd", 3), "/cd")

	// The trigger only fires at the very start of the line, so a slash inside
	// ordinary prose is left alone.
	require.Empty(t, labels("tell me about /re", len("tell me about /re")))

	// Ordinary prompt words yield no completions (only "/" and "$" do).
	require.Empty(t, labels("hello", 5))
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
