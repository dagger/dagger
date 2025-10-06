package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/waigani/diffparser"
)

func SummarizePatch(out *termenv.Output, patch string, maxWidth int) error {
	diff, err := diffparser.Parse(patch)
	if err != nil {
		return fmt.Errorf("parse patch: %w", err)
	}

	totalAdded := 0
	totalRemoved := 0

	var longestFilenameLen int
	for _, f := range diff.Files {
		filename := f.NewName
		if filename == "" {
			filename = f.OrigName
		}
		if filename == "" {
			// FIXME: likely an empty file was created, which yields:
			//
			//   diff --git b/hey b/hey
			//   new file mode 100644
			//   index 0000000..e69de29
			//
			// our diff parsing package doesn't handle 'new file' and parse the
			// filename
			args := strings.Fields(f.DiffHeader)
			lastArg := args[len(args)-1]
			_, filename, _ = strings.Cut(lastArg, "/")
			f.NewName = filename
		}
		if len(filename) > longestFilenameLen {
			longestFilenameLen = len(filename)
		}
	}

	var maxFilenameLen int
	if maxWidth > 0 {
		maxFilenameLen := max(maxWidth-20, 10) // Leave space for " | ", change count, and bars
		if longestFilenameLen > maxFilenameLen {
			longestFilenameLen = maxFilenameLen
		}
	}

	for _, f := range diff.Files {
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

		totalAdded += addedLines
		totalRemoved += removedLines

		// Determine filename and color
		var filenameColor termenv.Color
		isAdded := f.OrigName == ""
		isRemoved := f.NewName == ""

		filename := f.NewName
		if isRemoved {
			filename = f.OrigName
			filenameColor = termenv.ANSIRed
		} else if isAdded {
			filenameColor = termenv.ANSIGreen
		} else {
			filenameColor = termenv.ANSIYellow
		}

		// Shorten filename based on maxWidth
		if maxFilenameLen > 0 {
			if len(filename) > maxFilenameLen {
				filename = "..." + filename[len(filename)-(maxFilenameLen-3):]
			}
		}

		// Format line with colors
		out.WriteString(out.String(filename).Foreground(filenameColor).String())
		if len(filename) < longestFilenameLen {
			out.WriteString(strings.Repeat(" ", longestFilenameLen-len(filename)))
		}

		// Show change indicator
		if maxWidth > 0 {
			// Simplified text form for constrained width
			if addedLines > 0 {
				fmt.Fprintf(out, " %s", out.String(fmt.Sprintf("+%d", addedLines)).Foreground(termenv.ANSIGreen))
			}
			if removedLines > 0 {
				fmt.Fprintf(out, " %s", out.String(fmt.Sprintf("-%d", removedLines)).Foreground(termenv.ANSIRed))
			}
		} else {
			out.WriteString(" | ")

			// Absolute bars representation
			if addedLines > 0 {
				out.WriteString(out.String(strings.Repeat("+", addedLines)).Foreground(termenv.ANSIGreen).String())
			}
			if removedLines > 0 {
				out.WriteString(out.String(strings.Repeat("-", removedLines)).Foreground(termenv.ANSIRed).String())
			}
		}
		out.WriteString("\n")
	}

	// Add summary line
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%d %s changed", len(diff.Files),
		func() string {
			if len(diff.Files) == 1 {
				return "file"
			} else {
				return "files"
			}
		}(),
	)
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
