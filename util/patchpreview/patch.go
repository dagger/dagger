package patchpreview

import (
	"fmt"
	"slices"
	"strings"

	"github.com/muesli/termenv"
)

type Entry struct {
	Path    string
	OldPath string
	Kind    string
	Added   int
	Removed int
}

const (
	KindAdded    = "ADDED"
	KindModified = "MODIFIED"
	KindRemoved  = "REMOVED"
	KindRenamed  = "RENAMED"
)

// SummarizeString returns a plain-text diff summary (no ANSI colors).
func SummarizeString(entries []Entry, maxWidth int) string {
	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	Summarize(out, entries, maxWidth)
	return buf.String()
}

// Commit is a commit staged in the workspace but not yet saved to the local
// checkout, along with the changes it folded in.
type Commit struct {
	// SHA is the commit's full (or already shortened) hash.
	SHA string
	// Message is the commit message; only its first line is displayed.
	Message string
	// Entries is the diffstat of the changes this commit introduced.
	Entries []Entry
}

// ShortSHA returns the abbreviated commit hash used in the summary.
func (c Commit) ShortSHA() string {
	if len(c.SHA) > shortSHALen {
		return c.SHA[:shortSHALen]
	}
	return c.SHA
}

// Subject returns the first line of the commit message.
func (c Commit) Subject() string {
	subject, _, _ := strings.Cut(c.Message, "\n")
	return strings.TrimSpace(subject)
}

const (
	shortSHALen = 7
	// maxCommits is how many staged commits are rendered before the rest are
	// elided; the summary lives in a small bubble, not a scrollable pane.
	maxCommits = 5
)

// Summarize writes a colored diff summary to out. Removed files under removed
// directories are folded into a single entry. Does nothing if entries is empty.
func Summarize(out *termenv.Output, entries []Entry, maxWidth int) {
	if len(entries) == 0 {
		return
	}

	count, totalAdded, totalRemoved := writeEntries(out, entries, maxWidth, false)

	fileWord := "files"
	if count == 1 {
		fileWord = "file"
	}
	fmt.Fprintf(out, "\n%d %s changed", count, fileWord)
	if totalAdded+totalRemoved > 0 {
		fmt.Fprint(out, ",")
		if totalAdded > 0 {
			out.WriteString(out.String(fmt.Sprintf(" +%d", totalAdded)).Foreground(termenv.ANSIGreen).String())
		}
		if totalRemoved > 0 {
			out.WriteString(out.String(fmt.Sprintf(" -%d", totalRemoved)).Foreground(termenv.ANSIRed).String())
		}
		out.WriteString(" lines")
	}
}

// SummarizeChanges writes the uncommitted diff summary followed by the staged
// commits, newest commit first, separated by blank lines:
//
//	foo.txt +42
//
//	1 file changed, +42 lines
//
//	abcdef0 do thing
//	bar.txt -32
//
//	deadbee another commit
//	buzz.txt +1 -34
//
// commits are given oldest-first (as the API reports them) and are reversed
// here. Writes nothing when there is nothing to show.
func SummarizeChanges(out *termenv.Output, entries []Entry, commits []Commit, maxWidth int) {
	if len(entries) == 0 && len(commits) == 0 {
		return
	}

	sections := []string{}
	if len(entries) > 0 {
		sections = append(sections, renderSection(out, func(sub *termenv.Output) {
			Summarize(sub, entries, maxWidth)
		}))
	}

	// Newest commit first, so reading top-down goes from least to most settled.
	commits = slices.Clone(commits)
	slices.Reverse(commits)

	elided := 0
	if len(commits) > maxCommits {
		elided = len(commits) - maxCommits
		commits = commits[:maxCommits]
	}

	for _, commit := range commits {
		sections = append(sections, renderSection(out, func(sub *termenv.Output) {
			writeCommitHeader(sub, commit, maxWidth)
			writeEntries(sub, commit.Entries, maxWidth, true)
		}))
	}

	if elided > 0 {
		commitWord := "commits"
		if elided == 1 {
			commitWord = "commit"
		}
		sections = append(sections, renderSection(out, func(sub *termenv.Output) {
			sub.WriteString(sub.String(fmt.Sprintf("… %d more %s …", elided, commitWord)).
				Foreground(termenv.ANSIBrightBlack).Faint().String())
		}))
	}

	out.WriteString(strings.Join(sections, "\n\n"))
}

// renderSection captures the output of write into a string with trailing
// newlines trimmed, so sections can be joined with a blank line between them.
func renderSection(out *termenv.Output, write func(*termenv.Output)) string {
	var buf strings.Builder
	sub := termenv.NewOutput(&buf, termenv.WithProfile(out.Profile), termenv.WithTTY(true))
	write(sub)
	return strings.TrimRight(buf.String(), "\n")
}

// writeCommitHeader writes the "<short sha> <subject>" line for a staged commit.
func writeCommitHeader(out *termenv.Output, commit Commit, maxWidth int) {
	sha := commit.ShortSHA()
	out.WriteString(out.String(sha).Foreground(termenv.ANSIYellow).String())

	subject := commit.Subject()
	if subject == "" {
		out.WriteString("\n")
		return
	}
	if avail := maxWidth - len(sha) - 1; avail > 0 {
		subject = truncateMiddleString(subject, avail)
	}
	out.WriteString(" ")
	out.WriteString(out.String(subject).Bold().String())
	out.WriteString("\n")
}

// writeEntries writes one line per diffstat entry, returning the number of
// entries written (after folding) and the total added/removed line counts. When
// dim is set the lines are faint, so staged commits recede behind the
// uncommitted changes above them.
func writeEntries(out *termenv.Output, entries []Entry, maxWidth int, dim bool) (count, totalAdded, totalRemoved int) {
	if len(entries) == 0 {
		return 0, 0, 0
	}

	entries = foldRemovedDirs(entries)
	slices.SortFunc(entries, func(a, b Entry) int {
		return strings.Compare(a.Path, b.Path)
	})

	maxFilenameLen := max(maxWidth-20, 10)
	longestFilenameLen := 0
	for _, e := range entries {
		if l := len(entryLabel(e)); l > longestFilenameLen {
			longestFilenameLen = l
		}
	}
	if longestFilenameLen > maxFilenameLen {
		longestFilenameLen = maxFilenameLen
	}

	style := func(s string, color termenv.Color) string {
		st := out.String(s).Foreground(color)
		if dim {
			st = st.Faint()
		}
		return st.String()
	}

	for _, e := range entries {
		filename := truncateLabel(e, maxFilenameLen)

		var color termenv.Color
		switch e.Kind {
		case KindAdded:
			color = termenv.ANSIGreen
		case KindRemoved:
			color = termenv.ANSIRed
		default:
			color = termenv.ANSIYellow
		}

		totalAdded += e.Added
		totalRemoved += e.Removed

		out.WriteString(style(filename, color))
		if len(filename) < longestFilenameLen {
			out.WriteString(strings.Repeat(" ", longestFilenameLen-len(filename)))
		}
		if e.Added > 0 {
			fmt.Fprintf(out, " %s", style(fmt.Sprintf("+%d", e.Added), termenv.ANSIGreen))
		}
		if e.Removed > 0 {
			fmt.Fprintf(out, " %s", style(fmt.Sprintf("-%d", e.Removed), termenv.ANSIRed))
		}
		out.WriteString("\n")
	}

	return len(entries), totalAdded, totalRemoved
}

func entryLabel(e Entry) string {
	if e.Kind == KindRenamed && e.OldPath != "" {
		return e.OldPath + " => " + e.Path
	}
	return e.Path
}

func truncateLabel(e Entry, maxLen int) string {
	if e.Kind == KindRenamed && e.OldPath != "" {
		return truncateRenameLabel(e.OldPath, e.Path, maxLen)
	}
	return truncatePath(e.Path, maxLen)
}

func truncateRenameLabel(oldPath, newPath string, maxLen int) string {
	const sep = " => "

	if len(oldPath)+len(sep)+len(newPath) <= maxLen {
		return oldPath + sep + newPath
	}
	if maxLen <= len(sep)+2 {
		return truncateMiddleString(oldPath+sep+newPath, maxLen)
	}

	available := maxLen - len(sep)
	oldLen := available / 2
	newLen := available - oldLen
	return truncatePath(oldPath, oldLen) + sep + truncatePath(newPath, newLen)
}

func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	if maxLen <= 3 {
		return strings.Repeat(".", maxLen)
	}

	trailingSlash := ""
	if strings.HasSuffix(path, "/") {
		trailingSlash = "/"
		path = strings.TrimSuffix(path, "/")
	}

	parts := strings.Split(path, "/")
	if len(parts) >= 3 {
		label := parts[0] + "/.../" + parts[len(parts)-1] + trailingSlash
		if len(label) <= maxLen {
			return label
		}
	}

	if len(parts) >= 2 {
		label := ".../" + parts[len(parts)-1] + trailingSlash
		if len(label) <= maxLen {
			return label
		}
	}

	return truncateMiddleString(path+trailingSlash, maxLen)
}

func truncateMiddleString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return strings.Repeat(".", maxLen)
	}

	keep := maxLen - 3
	left := (keep + 1) / 2
	right := keep - left
	return s[:left] + "..." + s[len(s)-right:]
}

// foldRemovedDirs merges removed entries (files and subdirectories) into
// their topmost removed parent directory, summing line counts. E.g. if
// "dir/", "dir/sub/", and "dir/sub/a.txt" are all removed, only "dir/"
// is kept with the combined count.
func foldRemovedDirs(entries []Entry) []Entry {
	var allDirs []Entry
	for _, e := range entries {
		if e.Kind == KindRemoved && strings.HasSuffix(e.Path, "/") {
			allDirs = append(allDirs, e)
		}
	}
	if len(allDirs) == 0 {
		return entries
	}

	// Keep only topmost removed directories (discard children).
	var dirs []Entry
	for _, d := range allDirs {
		isChild := slices.ContainsFunc(allDirs, func(parent Entry) bool {
			return parent.Path != d.Path && strings.HasPrefix(d.Path, parent.Path)
		})
		if !isChild {
			dirs = append(dirs, d)
		}
	}

	var result []Entry
	for _, e := range entries {
		// Skip all removed directory entries; topmost ones re-added below.
		if e.Kind == KindRemoved && strings.HasSuffix(e.Path, "/") {
			continue
		}
		// Fold removed files into their parent directory.
		if e.Kind == KindRemoved {
			if idx := slices.IndexFunc(dirs, func(d Entry) bool {
				return strings.HasPrefix(e.Path, d.Path)
			}); idx >= 0 {
				dirs[idx].Removed += e.Removed
				continue
			}
		}
		result = append(result, e)
	}
	return append(result, dirs...)
}
