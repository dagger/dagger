package idtui

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
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

// diffLinePair pairs left/right columns for side-by-side display.
type diffLinePair struct {
	left  *diffLine
	right *diffLine
}

// diffContextLines is the number of context lines to show around changes.
const diffContextLines = 3

// renderEditDiff produces a side-by-side diff view for an edit tool call.
// It tries to read the file from the local filesystem for context and line
// numbers. If the file is unavailable it falls back to a plain old/new diff.
//
// fileName is used both for file I/O and to select a syntax lexer.
// totalWidth is the terminal width available for the entire diff.
func renderEditDiff(profile termenv.Profile, fileName, oldText, newText string, totalWidth int) string {
	if totalWidth < 40 {
		totalWidth = 40
	}

	// Compute the line-level diff between old and new text.
	lines := lineLevelDiff(oldText, newText)
	if len(lines) == 0 {
		return ""
	}

	// Try to add file context (real line numbers + surrounding lines).
	lines = addFileContext(fileName, oldText, lines)

	// Pair lines for side-by-side.
	pairs := pairDiffLines(lines)

	colWidth := totalWidth / 2
	leftW := colWidth
	rightW := totalWidth - colWidth

	var sb strings.Builder
	for _, p := range pairs {
		sb.WriteString(renderLeftCol(profile, fileName, p.left, p.right, leftW))
		sb.WriteString(renderRightCol(profile, fileName, p.right, p.left, rightW))
		sb.WriteByte('\n')
	}
	return sb.String()
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

// applyEmphasis wraps emphasised byte ranges in an already-ANSI-styled string
// with bold+underline escapes. It tracks ANSI sequences so emphasis is applied
// to the correct visible characters.
func applyEmphasis(styled string, ranges []emphRange) string {
	if len(ranges) == 0 {
		return styled
	}

	var sb strings.Builder
	sb.Grow(len(styled) + len(ranges)*20)

	rawIdx := 0 // byte position in the original raw content
	inEmph := false

	for i := 0; i < len(styled); {
		// Skip over ANSI escape sequences.
		if styled[i] == '\x1b' {
			j := i + 1
			for j < len(styled) && !((styled[j] >= 'A' && styled[j] <= 'Z') || (styled[j] >= 'a' && styled[j] <= 'z')) {
				j++
			}
			if j < len(styled) {
				j++ // include the final letter
			}
			sb.WriteString(styled[i:j])
			i = j
			continue
		}

		// Visible character — check emphasis transitions.
		shouldEmph := false
		for _, r := range ranges {
			if rawIdx >= r.Start && rawIdx < r.End {
				shouldEmph = true
				break
			}
		}

		if shouldEmph && !inEmph {
			sb.WriteString("\x1b[1;4m") // bold + underline
			inEmph = true
		} else if !shouldEmph && inEmph {
			sb.WriteString("\x1b[22;24m") // reset bold + underline
			inEmph = false
		}

		sb.WriteByte(styled[i])
		rawIdx++
		i++
	}

	if inEmph {
		sb.WriteString("\x1b[22;24m")
	}

	return sb.String()
}

// pairDiffLines groups lines into left/right pairs for side-by-side display.
func pairDiffLines(lines []diffLine) []diffLinePair {
	var pairs []diffLinePair
	i := 0
	for i < len(lines) {
		switch lines[i].Kind {
		case diffRemoved:
			// Collect consecutive removed lines.
			remStart := i
			for i < len(lines) && lines[i].Kind == diffRemoved {
				i++
			}
			// Collect consecutive added lines that follow.
			addStart := i
			for i < len(lines) && lines[i].Kind == diffAdded {
				i++
			}
			remCount := addStart - remStart
			addCount := i - addStart

			// Pair them up: min(rem, add) paired rows, then leftovers.
			j := 0
			for j < remCount && j < addCount {
				pairs = append(pairs, diffLinePair{left: &lines[remStart+j], right: &lines[addStart+j]})
				j++
			}
			for j < remCount {
				pairs = append(pairs, diffLinePair{left: &lines[remStart+j]})
				j++
			}
			for k := j; k < addCount; k++ {
				pairs = append(pairs, diffLinePair{right: &lines[addStart+k]})
			}
		case diffAdded:
			pairs = append(pairs, diffLinePair{right: &lines[i]})
			i++
		default: // context
			pairs = append(pairs, diffLinePair{left: &lines[i], right: &lines[i]})
			i++
		}
	}
	return pairs
}

// ── column renderers ────────────────────────────────────────────────────

func renderLeftCol(profile termenv.Profile, fileName string, dl *diffLine, paired *diffLine, width int) string {
	if dl == nil {
		return strings.Repeat(" ", width)
	}
	return renderCol(profile, fileName, dl, paired, dl.OldNo, width, true)
}

func renderRightCol(profile termenv.Profile, fileName string, dl *diffLine, paired *diffLine, width int) string {
	if dl == nil {
		return strings.Repeat(" ", width)
	}
	return renderCol(profile, fileName, dl, paired, dl.NewNo, width, false)
}

func renderCol(profile termenv.Profile, fileName string, dl *diffLine, paired *diffLine, lineNo int, width int, isLeft bool) string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))

	// Line number gutter: "NNNN " (5 chars) + marker "X " (2 chars) = 7 chars.
	var gutter string
	if lineNo > 0 {
		gutter = fmt.Sprintf("%4d ", lineNo)
	} else {
		gutter = "     "
	}

	var marker string
	var lineColor termenv.Color
	switch dl.Kind {
	case diffRemoved:
		if isLeft {
			marker = "- "
			lineColor = termenv.ANSIRed
		} else {
			marker = "  "
			lineColor = termenv.ANSIBrightBlack
		}
	case diffAdded:
		if !isLeft {
			marker = "+ "
			lineColor = termenv.ANSIGreen
		} else {
			marker = "  "
			lineColor = termenv.ANSIBrightBlack
		}
	default:
		marker = "  "
		lineColor = termenv.ANSIBrightBlack
	}

	gutterStr := out.String(gutter).Foreground(lineColor).Faint().String()
	markerStr := out.String(marker).Foreground(lineColor).String()

	// Syntax-highlight the content.
	content := syntaxHighlightLine(fileName, dl.Content)

	// Apply intra-line emphasis for paired removed/added lines.
	if paired != nil && dl.Kind == diffRemoved && paired.Kind == diffAdded && isLeft {
		oldRanges, _ := intralineRanges(dl.Content, paired.Content)
		content = applyEmphasis(content, oldRanges)
	} else if paired != nil && dl.Kind == diffAdded && paired.Kind == diffRemoved && !isLeft {
		_, newRanges := intralineRanges(paired.Content, dl.Content)
		content = applyEmphasis(content, newRanges)
	}

	const gutterW = 7 // "NNNN " + "X "
	contentW := width - gutterW
	if contentW < 1 {
		contentW = 1
	}

	// Truncate content to fit.
	truncated := ansi.Truncate(content, contentW, "…")

	line := gutterStr + markerStr + truncated

	// Pad to full column width with spaces.
	lineVisW := ansi.StringWidth(line)
	if lineVisW < width {
		line += strings.Repeat(" ", width-lineVisW)
	}

	return line
}

// syntaxHighlightLine highlights a single line of code using chroma.
func syntaxHighlightLine(fileName, line string) string {
	if line == "" {
		return ""
	}
	lexer := lexers.Match(fileName)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	formatter := formatters.Get("terminal16")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	style := TTYStyle()

	it, err := lexer.Tokenise(nil, line)
	if err != nil {
		return line
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, it); err != nil {
		return line
	}

	// Chroma may append a trailing newline; strip it.
	return strings.TrimRight(buf.String(), "\n")
}
