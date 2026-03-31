package patchpreview

import (
	"fmt"
	"slices"
	"strings"

	"github.com/muesli/termenv"
)

type Entry struct {
	Path    string
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

// Summarize writes a colored diff summary to out. Removed files under removed
// directories are folded into a single entry. Does nothing if entries is empty.
func Summarize(out *termenv.Output, entries []Entry, maxWidth int) {
	if len(entries) == 0 {
		return
	}

	entries = foldRemovedDirs(entries)
	slices.SortFunc(entries, func(a, b Entry) int {
		return strings.Compare(a.Path, b.Path)
	})

	maxFilenameLen := max(maxWidth-20, 10)
	longestFilenameLen := 0
	for _, e := range entries {
		if l := len(e.Path); l > longestFilenameLen {
			longestFilenameLen = l
		}
	}
	if longestFilenameLen > maxFilenameLen {
		longestFilenameLen = maxFilenameLen
	}

	var totalAdded, totalRemoved int
	for _, e := range entries {
		filename := e.Path
		if len(filename) > maxFilenameLen {
			filename = "..." + filename[len(filename)-(maxFilenameLen-3):]
		}

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

		out.WriteString(out.String(filename).Foreground(color).String())
		if len(filename) < longestFilenameLen {
			out.WriteString(strings.Repeat(" ", longestFilenameLen-len(filename)))
		}
		if e.Added > 0 {
			fmt.Fprintf(out, " %s", out.String(fmt.Sprintf("+%d", e.Added)).Foreground(termenv.ANSIGreen))
		}
		if e.Removed > 0 {
			fmt.Fprintf(out, " %s", out.String(fmt.Sprintf("-%d", e.Removed)).Foreground(termenv.ANSIRed))
		}
		out.WriteString("\n")
	}

	fileWord := "files"
	if len(entries) == 1 {
		fileWord = "file"
	}
	fmt.Fprintf(out, "\n%d %s changed", len(entries), fileWord)
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
