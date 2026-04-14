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

func TestSummarizeRename(t *testing.T) {
	entries := []Entry{
		{Path: "new.txt", OldPath: "old.txt", Kind: KindRenamed, Added: 2, Removed: 3},
	}

	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, entries, 80)

	text := buf.String()
	require.Contains(t, text, "old.txt => new.txt")
	require.Contains(t, text, "+2")
	require.Contains(t, text, "-3")
	require.Contains(t, text, "1 file changed")
}

func TestTruncateLabelPathAware(t *testing.T) {
	got := truncateLabel(Entry{Path: "alpha/beta/gamma/delta.txt"}, 20)
	require.Equal(t, "alpha/.../delta.txt", got)
}

func TestTruncateLabelRenameAware(t *testing.T) {
	got := truncateLabel(Entry{
		Path:    "after/four/five/six.txt",
		OldPath: "before/one/two/three.txt",
		Kind:    KindRenamed,
	}, 40)

	require.Contains(t, got, " => ")
	parts := strings.Split(got, " => ")
	require.Len(t, parts, 2)
	require.Contains(t, parts[0], "...")
	require.Contains(t, parts[1], "...")
	require.LessOrEqual(t, len(got), 40)
}

func TestSummarizeEmpty(t *testing.T) {
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, nil, 80)
	require.Empty(t, buf.String())
	Summarize(out, []Entry{}, 80)
	require.Empty(t, buf.String())
}
