package patchpreview

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestSummarize(t *testing.T) {
	entries := []Entry{
		{Path: "mod.txt", Kind: "MODIFIED", Added: 1, Removed: 1},
		{Path: "new.txt", Kind: "ADDED", Added: 1},
		{Path: "old.txt", Kind: "REMOVED", Removed: 1},
		{Path: "removed-dir/", Kind: "REMOVED"},
		{Path: "removed-dir/file.txt", Kind: "REMOVED", Removed: 2},
	}

	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, entries, 80)

	text := buf.String()
	require.Contains(t, text, "mod.txt")
	require.Contains(t, text, "new.txt")
	require.Contains(t, text, "old.txt")
	require.Contains(t, text, "removed-dir/")
	require.NotContains(t, text, "removed-dir/file.txt")
	require.Contains(t, text, "4 files changed")
	require.Contains(t, text, "+2")
	require.Contains(t, text, "-4")
}

func TestSummarizeEmpty(t *testing.T) {
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, nil, 80)
	require.Empty(t, buf.String())
	Summarize(out, []Entry{}, 80)
	require.Empty(t, buf.String())
}
