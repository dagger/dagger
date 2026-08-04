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

func TestSummarizeEmpty(t *testing.T) {
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, nil, 80)
	require.Empty(t, buf.String())
	Summarize(out, []Entry{}, 80)
	require.Empty(t, buf.String())
}

func summarizeChangesString(t *testing.T, entries []Entry, commits []Commit) string {
	t.Helper()
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	SummarizeChanges(out, entries, commits, 80)
	return buf.String()
}

func TestSummarizeChangesEmpty(t *testing.T) {
	require.Empty(t, summarizeChangesString(t, nil, nil))
}

func TestSummarizeChangesOnlyUncommitted(t *testing.T) {
	got := summarizeChangesString(t, []Entry{
		{Path: "foo.txt", Kind: KindModified, Added: 42},
	}, nil)

	require.Equal(t, "foo.txt +42\n\n1 file changed, +42 lines", got)
}

func TestSummarizeChangesOnlyCommits(t *testing.T) {
	got := summarizeChangesString(t, nil, []Commit{
		{
			SHA:     "deadbeefcafe",
			Message: "another commit",
			Entries: []Entry{{Path: "buzz.txt", Kind: KindModified, Added: 1, Removed: 34}},
		},
		{
			SHA:     "abcdef1234567",
			Message: "do thing\n\nwith a body that must not show up",
			Entries: []Entry{{Path: "bar.txt", Kind: KindModified, Removed: 32}},
		},
	})

	// Newest commit (last in the API order) comes first.
	require.Equal(t, strings.Join([]string{
		"abcdef1 do thing",
		"bar.txt -32",
		"",
		"deadbee another commit",
		"buzz.txt +1 -34",
	}, "\n"), got)
	require.NotContains(t, got, "with a body")
}

func TestSummarizeChangesBoth(t *testing.T) {
	got := summarizeChangesString(t, []Entry{
		{Path: "foo.txt", Kind: KindModified, Added: 42},
	}, []Commit{
		{
			SHA:     "abcdef1234567",
			Message: "do thing",
			Entries: []Entry{{Path: "bar.txt", Kind: KindModified, Removed: 32}},
		},
	})

	require.Equal(t, strings.Join([]string{
		"foo.txt +42",
		"",
		"1 file changed, +42 lines",
		"",
		"abcdef1 do thing",
		"bar.txt -32",
	}, "\n"), got)
}

func TestSummarizeChangesElidesOldCommits(t *testing.T) {
	var commits []Commit
	for i := range maxCommits + 3 {
		commits = append(commits, Commit{
			SHA:     fmt.Sprintf("%07d000", i),
			Message: fmt.Sprintf("commit %d", i),
			Entries: []Entry{{Path: fmt.Sprintf("f%d.txt", i), Kind: KindAdded, Added: 1}},
		})
	}

	got := summarizeChangesString(t, nil, commits)

	// Newest maxCommits are shown, oldest are elided.
	require.Contains(t, got, "commit 7")
	require.Contains(t, got, "commit 3")
	require.NotContains(t, got, "commit 2")
	require.NotContains(t, got, "commit 0")
	require.Contains(t, got, "… 3 more commits …")
	require.Equal(t, maxCommits, strings.Count(got, "commit "))

	// The elision line is last.
	lines := strings.Split(got, "\n")
	require.Equal(t, "… 3 more commits …", lines[len(lines)-1])
}

func TestSummarizeChangesSingleElidedCommit(t *testing.T) {
	var commits []Commit
	for i := range maxCommits + 1 {
		commits = append(commits, Commit{
			SHA:     fmt.Sprintf("%07d000", i),
			Message: fmt.Sprintf("commit %d", i),
		})
	}

	require.Contains(t, summarizeChangesString(t, nil, commits), "… 1 more commit …")
}

func TestSummarizeChangesEmptySubject(t *testing.T) {
	got := summarizeChangesString(t, nil, []Commit{
		{SHA: "abcdef1234567", Entries: []Entry{{Path: "bar.txt", Kind: KindAdded, Added: 2}}},
	})

	require.Equal(t, "abcdef1\nbar.txt +2", got)
}
