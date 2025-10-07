package idtui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/jedevc/diffparser"
	"github.com/muesli/termenv"
)

type PatchPreview struct {
	Patch       *diffparser.Diff
	AddedDirs   []string
	RemovedDirs []string
}

func PreviewPatch(ctx context.Context, changeset *dagger.Changeset) (*PatchPreview, error) {
	rawPatch, err := changeset.AsPatch().Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("get patch: %w", err)
	}
	addedDirectories, err := changeset.AddedPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("get added paths: %w", err)
	}
	addedDirectories = slices.DeleteFunc(addedDirectories, func(s string) bool {
		return !strings.HasSuffix(s, "/")
	})
	removedDirectories, err := changeset.RemovedPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("get removed paths: %w", err)
	}
	removedDirectories = slices.DeleteFunc(removedDirectories, func(s string) bool {
		return !strings.HasSuffix(s, "/")
	})

	if rawPatch == "" && len(addedDirectories) == 0 && len(removedDirectories) == 0 {
		// No changes
		return nil, nil
	}

	patch, err := diffparser.Parse(rawPatch)
	if err != nil {
		return nil, fmt.Errorf("parse patch: %w", err)
	}

	return &PatchPreview{
		Patch:       patch,
		AddedDirs:   addedDirectories,
		RemovedDirs: removedDirectories,
	}, nil
}

func (preview *PatchPreview) Summarize(out *termenv.Output, maxWidth int) error {
	lines := preview.lines()

	longestFilenameLen := 0
	for _, line := range lines {
		if len(line.filename) > longestFilenameLen {
			longestFilenameLen = len(line.filename)
		}
	}
	var maxFilenameLen int
	if maxWidth > 0 {
		maxFilenameLen := max(maxWidth-20, 10) // Leave space for " | ", change count, and bars
		if longestFilenameLen > maxFilenameLen {
			longestFilenameLen = maxFilenameLen
		}
	}

	totalAdded := 0
	totalRemoved := 0

	for _, line := range lines {
		filename := shortenPath(line.filename, maxFilenameLen)

		var filenameColor termenv.Color
		switch line.mode {
		case diffparser.NEW:
			filenameColor = termenv.ANSIGreen
		case diffparser.DELETED:
			filenameColor = termenv.ANSIRed
		case diffparser.MODIFIED, diffparser.RENAMED:
			filenameColor = termenv.ANSIYellow
		}

		totalAdded += line.added
		totalRemoved += line.removed

		// Format line with colors
		out.WriteString(out.String(filename).Foreground(filenameColor).String())
		if len(filename) < longestFilenameLen {
			out.WriteString(strings.Repeat(" ", longestFilenameLen-len(filename)))
		}

		// Show change indicator
		if maxWidth > 0 {
			// Simplified text form for constrained width
			if line.added > 0 {
				fmt.Fprintf(out, " %s", out.String(fmt.Sprintf("+%d", line.added)).Foreground(termenv.ANSIGreen))
			}
			if line.removed > 0 {
				fmt.Fprintf(out, " %s", out.String(fmt.Sprintf("-%d", line.removed)).Foreground(termenv.ANSIRed))
			}
		} else {
			out.WriteString(" | ")

			// Absolute bars representation
			if line.added > 0 {
				out.WriteString(out.String(strings.Repeat("+", line.added)).Foreground(termenv.ANSIGreen).String())
			}
			if line.removed > 0 {
				out.WriteString(out.String(strings.Repeat("-", line.removed)).Foreground(termenv.ANSIRed).String())
			}
		}
		out.WriteString("\n")
	}

	// Add total summary line
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%d %s changed", len(lines), pluralize(len(lines), "file", "files"))
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

	return nil
}

type patchPreviewLine struct {
	filename string
	mode     diffparser.FileMode
	added    int
	removed  int
}

func (preview *PatchPreview) lines() []patchPreviewLine {
	addedDirs := make([]patchPreviewLine, 0, len(preview.AddedDirs))
	for _, filename := range preview.AddedDirs {
		addedDirs = append(addedDirs, patchPreviewLine{filename: filename, mode: diffparser.NEW})
	}

	removedDirs := make([]patchPreviewLine, 0, len(preview.RemovedDirs))
	for _, filename := range preview.RemovedDirs {
		removedDirs = append(removedDirs, patchPreviewLine{filename: filename, mode: diffparser.DELETED})
	}

	previews := make([]patchPreviewLine, 0, len(preview.Patch.Files)+len(preview.AddedDirs)+len(preview.RemovedDirs))
loop:
	for _, f := range preview.Patch.Files {
		filename := cmp.Or(f.NewName, f.OrigName)

		var removedLines, addedLines int
		for _, h := range f.Hunks {
			for _, l := range h.WholeRange.Lines {
				switch l.Mode {
				case diffparser.ADDED:
					addedLines++
				case diffparser.REMOVED:
					removedLines++
				}
			}
		}

		// consolidate into removed dirs (avoids listing every file in a removed dir)
		if f.Mode == diffparser.DELETED {
			for i, dir := range removedDirs {
				if strings.HasPrefix(filename, dir.filename) {
					dir.removed += removedLines
					removedDirs[i] = dir
					continue loop
				}
			}
		}

		previews = append(previews, patchPreviewLine{
			filename: filename,
			mode:     f.Mode,
			added:    addedLines,
			removed:  removedLines,
		})
	}

	previews = append(previews, addedDirs...)
	previews = append(previews, removedDirs...)

	slices.SortFunc(previews, func(a, b patchPreviewLine) int {
		return strings.Compare(a.filename, b.filename)
	})

	return previews
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func shortenPath(filename string, maxFilenameLen int) string {
	if maxFilenameLen == 0 {
		return filename
	}
	if len(filename) > maxFilenameLen {
		filename = "..." + filename[len(filename)-(maxFilenameLen-3):]
	}
	return filename
}
