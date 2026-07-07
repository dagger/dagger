package idtui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// diffLineKind classifies a line in a diff.
type diffLineKind int

const (
	diffContext diffLineKind = iota
	diffAdded
	diffRemoved
)

// diffLine is a single line in a diff hunk.
type diffLine struct {
	OldNo   int          // line number in old file (0 = absent)
	NewNo   int          // line number in new file (0 = absent)
	Kind    diffLineKind // context / added / removed
	Content string       // raw text (no newline)
}

// diffContextLines is the number of context lines to show around changes.
const diffContextLines = 3

// diffTabWidth is the number of spaces used to expand tab characters in diff
// content. Tabs must be expanded because ansi.StringWidth (and Truncate)
// treat them as zero-width, but terminals render them at 8-column tab stops.
const diffTabWidth = 4

// renderEditDiff produces a unified diff view for an edit tool call.
// It tries to read the file from the local filesystem for context and line
// numbers. If the file is unavailable it falls back to a plain old/new diff.
//
// fileName is used for file I/O and context lookup.
// totalWidth is the terminal width available for the entire diff.
func renderEditDiff(profile termenv.Profile, fileName, oldText, newText string, totalWidth int) string {
	if totalWidth < 20 {
		totalWidth = 20
	}

	// Compute the line-level diff between old and new text.
	lines := lineLevelDiff(oldText, newText)
	if len(lines) == 0 {
		return ""
	}

	// Try to add file context (real line numbers + surrounding lines).
	lines = addFileContext(fileName, oldText, lines)

	// Pair consecutive removed/added lines for intraline highlighting.
	paired := pairForIntraline(lines)

	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))
	var sb strings.Builder
	for i, dl := range lines {
		sb.WriteString(renderUnifiedLine(out, dl, paired[i], totalWidth))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// renderUnifiedLine renders a single unified diff line with gutter, marker,
// and optional intraline emphasis, truncated to fit within width.
func renderUnifiedLine(out TermOutput, dl diffLine, pair *diffLine, width int) string {
	// Expand tabs to spaces so width math is correct.
	content := expandTabs(dl.Content, diffTabWidth)

	// Gutter: "NNNN NNNN " (10 chars) + marker "X " (2 chars) = 12 chars.
	const gutterW = 12
	var gutter string
	if dl.OldNo > 0 && dl.NewNo > 0 {
		gutter = fmt.Sprintf("%4d %4d ", dl.OldNo, dl.NewNo)
	} else if dl.OldNo > 0 {
		gutter = fmt.Sprintf("%4d      ", dl.OldNo)
	} else if dl.NewNo > 0 {
		gutter = fmt.Sprintf("     %4d ", dl.NewNo)
	} else {
		gutter = "          "
	}

	var marker string
	var lineColor termenv.Color
	switch dl.Kind {
	case diffRemoved:
		marker = "- "
		lineColor = termenv.ANSIRed
	case diffAdded:
		marker = "+ "
		lineColor = termenv.ANSIGreen
	default:
		marker = "  "
		lineColor = termenv.ANSIBrightBlack
	}

	gutterStr := out.String(gutter).Foreground(lineColor).Faint().String()
	markerStr := out.String(marker).Foreground(lineColor).String()

	// Apply intraline emphasis if we have a paired line.
	var styled string
	if pair != nil {
		pairedContent := expandTabs(pair.Content, diffTabWidth)
		if dl.Kind == diffRemoved {
			oldRanges, _ := intralineRanges(content, pairedContent)
			styled = applyIntralineColor(out, content, oldRanges, lineColor)
		} else if dl.Kind == diffAdded {
			_, newRanges := intralineRanges(pairedContent, content)
			styled = applyIntralineColor(out, content, newRanges, lineColor)
		}
	}
	if styled == "" {
		// No intraline — just color the whole content line.
		if dl.Kind != diffContext {
			styled = out.String(content).Foreground(lineColor).String()
		} else {
			styled = content
		}
	}

	contentW := width - gutterW
	if contentW < 1 {
		contentW = 1
	}

	truncated := ansi.Truncate(styled, contentW, "…")
	return gutterStr + markerStr + truncated
}

// applyIntralineColor colors the entire line in lineColor and applies
// bold+underline emphasis to the changed byte ranges.
func applyIntralineColor(out TermOutput, content string, ranges []emphRange, lineColor termenv.Color) string {
	if len(ranges) == 0 {
		return out.String(content).Foreground(lineColor).String()
	}

	var sb strings.Builder
	sb.Grow(len(content) + len(ranges)*30)

	pos := 0
	for _, r := range ranges {
		// Unchanged portion before this range.
		if r.Start > pos {
			sb.WriteString(out.String(content[pos:r.Start]).Foreground(lineColor).String())
		}
		// Emphasised (changed) portion: bold + underline.
		if r.End > r.Start {
			sb.WriteString(out.String(content[r.Start:r.End]).Foreground(lineColor).Bold().Underline().String())
		}
		pos = r.End
	}
	// Remainder after last range.
	if pos < len(content) {
		sb.WriteString(out.String(content[pos:]).Foreground(lineColor).String())
	}
	return sb.String()
}

// pairForIntraline returns a parallel slice where each entry is the
// "partner" line for intraline diffing, or nil. Consecutive removed lines
// are paired 1:1 with the consecutive added lines that follow them.
func pairForIntraline(lines []diffLine) []*diffLine {
	pairs := make([]*diffLine, len(lines))
	i := 0
	for i < len(lines) {
		if lines[i].Kind != diffRemoved {
			i++
			continue
		}
		remStart := i
		for i < len(lines) && lines[i].Kind == diffRemoved {
			i++
		}
		addStart := i
		for i < len(lines) && lines[i].Kind == diffAdded {
			i++
		}
		remCount := addStart - remStart
		addCount := i - addStart
		// Pair up 1:1.
		n := remCount
		if addCount < n {
			n = addCount
		}
		for j := range n {
			pairs[remStart+j] = &lines[addStart+j]
			pairs[addStart+j] = &lines[remStart+j]
		}
	}
	return pairs
}

// lineLevelDiff uses diffmatchpatch's line-mode diff to produce properly
// interleaved context/added/removed lines between oldText and newText.
func lineLevelDiff(oldText, newText string) []diffLine {
	dmp := diffmatchpatch.New()

	// Convert to line-level tokens for efficient diff.
	chars1, chars2, lineArray := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffMain(chars1, chars2, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var out []diffLine
	oldNo := 1
	newNo := 1

	for _, d := range diffs {
		// Split text into lines. diffmatchpatch includes trailing newlines
		// in each chunk, so we need to handle that carefully.
		text := d.Text
		// Remove a single trailing newline to avoid an empty extra line.
		text = strings.TrimSuffix(text, "\n")
		lines := strings.Split(text, "\n")

		switch d.Type {
		case diffmatchpatch.DiffEqual:
			for _, l := range lines {
				out = append(out, diffLine{OldNo: oldNo, NewNo: newNo, Kind: diffContext, Content: l})
				oldNo++
				newNo++
			}
		case diffmatchpatch.DiffDelete:
			for _, l := range lines {
				out = append(out, diffLine{OldNo: oldNo, Kind: diffRemoved, Content: l})
				oldNo++
			}
		case diffmatchpatch.DiffInsert:
			for _, l := range lines {
				out = append(out, diffLine{NewNo: newNo, Kind: diffAdded, Content: l})
				newNo++
			}
		}
	}

	return out
}

// addFileContext tries to read fileName, find oldText within it, and adjust
// line numbers to match the real file. It also prepends/appends a few context
// lines from the surrounding file. On any failure it returns lines unchanged.
func addFileContext(fileName, oldText string, lines []diffLine) []diffLine {
	if fileName == "" || oldText == "" {
		return lines
	}
	data, err := os.ReadFile(fileName)
	if err != nil {
		return lines
	}
	content := string(data)
	idx := strings.Index(content, oldText)
	if idx < 0 {
		return lines
	}

	// The first line of oldText starts at this 0-based line number in the file.
	fileStartLine := strings.Count(content[:idx], "\n") // 0-based

	// Shift all line numbers by fileStartLine (lines currently start at 1).
	for i := range lines {
		if lines[i].OldNo > 0 {
			lines[i].OldNo += fileStartLine
		}
		if lines[i].NewNo > 0 {
			lines[i].NewNo += fileStartLine
		}
	}

	// Prepend context lines from before the edit.
	allFileLines := strings.Split(content, "\n")
	ctxStart := fileStartLine - diffContextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	var before []diffLine
	for lineIdx := ctxStart; lineIdx < fileStartLine; lineIdx++ {
		lineNo := lineIdx + 1 // 1-based
		before = append(before, diffLine{
			OldNo: lineNo, NewNo: lineNo, Kind: diffContext,
			Content: allFileLines[lineIdx],
		})
	}

	// Append context lines from after the edit.
	// Find the end of the old text in the file.
	oldEndLine := fileStartLine + strings.Count(oldText, "\n") + 1 // exclusive, 0-based
	ctxEnd := oldEndLine + diffContextLines
	if ctxEnd > len(allFileLines) {
		ctxEnd = len(allFileLines)
	}

	// We need the line number of the last line in our diff to compute the
	// after-context line numbers correctly.
	lastOld := 0
	lastNew := 0
	for _, dl := range lines {
		if dl.OldNo > lastOld {
			lastOld = dl.OldNo
		}
		if dl.NewNo > lastNew {
			lastNew = dl.NewNo
		}
	}

	var after []diffLine
	for lineIdx := oldEndLine; lineIdx < ctxEnd; lineIdx++ {
		lastOld++
		lastNew++
		after = append(after, diffLine{
			OldNo: lastOld, NewNo: lastNew, Kind: diffContext,
			Content: allFileLines[lineIdx],
		})
	}

	result := make([]diffLine, 0, len(before)+len(lines)+len(after))
	result = append(result, before...)
	result = append(result, lines...)
	result = append(result, after...)
	return result
}

// emphRange marks a byte range in raw content that should be emphasised.
type emphRange struct {
	Start, End int
}

// intralineRanges computes character-level diff ranges between two lines.
func intralineRanges(oldContent, newContent string) (oldRanges, newRanges []emphRange) {
	dmp := diffmatchpatch.New()
	patches := dmp.DiffMain(oldContent, newContent, false)
	patches = dmp.DiffCleanupSemantic(patches)

	oldPos := 0
	newPos := 0
	for _, p := range patches {
		switch p.Type {
		case diffmatchpatch.DiffEqual:
			oldPos += len(p.Text)
			newPos += len(p.Text)
		case diffmatchpatch.DiffDelete:
			oldRanges = append(oldRanges, emphRange{oldPos, oldPos + len(p.Text)})
			oldPos += len(p.Text)
		case diffmatchpatch.DiffInsert:
			newRanges = append(newRanges, emphRange{newPos, newPos + len(p.Text)})
			newPos += len(p.Text)
		}
	}
	return
}

// expandTabs replaces tab characters with spaces, advancing to the next
// tab stop of the given width (like a terminal would). This ensures that
// width measurement and truncation functions see the true visual width.
func expandTabs(s string, tabWidth int) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8) // small extra for typical expansion
	col := 0
	for _, r := range s {
		if r == '\t' {
			spaces := tabWidth - (col % tabWidth)
			for range spaces {
				sb.WriteByte(' ')
			}
			col += spaces
		} else {
			sb.WriteRune(r)
			col++
		}
	}
	return sb.String()
}
