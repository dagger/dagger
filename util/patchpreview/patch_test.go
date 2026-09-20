package patchpreview

import (
	"fmt"
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

func TestSummarizeUsesActualDiffstatWidth(t *testing.T) {
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, []Entry{
		{Path: "commit-file-name.go", Kind: KindModified, Added: 46},
		{Path: "another-file-name.go", Kind: KindModified, Added: 1, Removed: 2},
	}, 26) // The narrowest Changes bubble has 26 columns for content.

	// The widest diffstat suffix (" +1 -2") is 6 columns, so 20 columns
	// remain for filenames — enough to show both without truncation.
	require.Equal(t, strings.Join([]string{
		"another-file-name.go +1 -2",
		"commit-file-name.go  +46",
		"",
		"2 files changed, +47 -2 lines",
	}, "\n"), buf.String())
}

func TestSummarizeEmpty(t *testing.T) {
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, nil, 80)
	require.Empty(t, buf.String())
	Summarize(out, []Entry{}, 80)
	require.Empty(t, buf.String())
}

func TestSummarizeCapsEntries(t *testing.T) {
	entries := make([]Entry, 0, MaxEntries+25)
	for i := range MaxEntries + 25 {
		entries = append(entries, Entry{
			Path:    fmt.Sprintf("dir/file-%03d.txt", i),
			Kind:    KindModified,
			Added:   1,
			Removed: 2,
		})
	}

	text := SummarizeString(entries, 80)
	lines := strings.Split(text, "\n")

	// The first MaxEntries entries (in sorted order) are listed, then the
	// overflow marker, a blank line, and the footer.
	require.Equal(t, MaxEntries+3, len(lines))
	for i := range MaxEntries {
		require.Contains(t, lines[i], fmt.Sprintf("dir/file-%03d.txt", i))
	}
	require.Equal(t, "… and 25 more files", lines[MaxEntries])
	require.NotContains(t, text, fmt.Sprintf("dir/file-%03d.txt", MaxEntries))

	// The footer counts every entry, not just the ones shown.
	require.Equal(t, fmt.Sprintf("%d files changed, +%d -%d lines", MaxEntries+25, MaxEntries+25, 2*(MaxEntries+25)), lines[len(lines)-1])
}

func TestSummarizeCapCountsFoldedEntries(t *testing.T) {
	// A removed directory and its files fold into one entry, which is what
	// the cap counts: MaxEntries files plus a folded directory overflows by
	// exactly one.
	entries := []Entry{
		{Path: "removed/", Kind: KindRemoved},
		{Path: "removed/a.txt", Kind: KindRemoved, Removed: 1},
		{Path: "removed/b.txt", Kind: KindRemoved, Removed: 1},
	}
	for i := range MaxEntries {
		entries = append(entries, Entry{Path: fmt.Sprintf("kept-%03d.txt", i), Kind: KindAdded, Added: 1})
	}

	text := SummarizeString(entries, 80)
	require.Contains(t, text, "… and 1 more file\n")
	require.NotContains(t, text, "removed/")
	require.Contains(t, text, fmt.Sprintf("%d files changed, +%d -2 lines", MaxEntries+1, MaxEntries))
}

func TestSummarizeAtCapShowsAll(t *testing.T) {
	entries := make([]Entry, 0, MaxEntries)
	for i := range MaxEntries {
		entries = append(entries, Entry{Path: fmt.Sprintf("file-%03d.txt", i), Kind: KindAdded, Added: 1})
	}

	text := SummarizeString(entries, 80)
	require.NotContains(t, text, "more file")
	require.Contains(t, text, fmt.Sprintf("file-%03d.txt", MaxEntries-1))
	require.Contains(t, text, fmt.Sprintf("%d files changed", MaxEntries))
}
