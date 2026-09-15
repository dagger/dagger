package idtui

import (
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// highlightDiff applies language-aware syntax highlighting to unified Git
// patches. The old and new sides of each hunk are tokenised independently so
// diff markers do not interfere with the source lexer.
func highlightDiff(profile termenv.Profile, patch string) string {
	if profile == termenv.Ascii || patch == "" {
		return patch
	}

	parsed, ok := parseDiff(patch)
	if !ok {
		return patch
	}

	for i := range parsed.files {
		file := &parsed.files[i]
		lexer := matchDiffLexer(file.name)
		if lexer == nil {
			continue
		}
		for j := range file.hunks {
			highlightDiffSide(parsed.lines, &file.hunks[j].old, lexer)
			highlightDiffSide(parsed.lines, &file.hunks[j].new, lexer)
		}
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))
	var rendered strings.Builder
	for _, line := range parsed.lines {
		switch line.kind {
		case diffLineAdded:
			rendered.WriteString(out.String("+").Foreground(termenv.ANSIGreen).String())
			rendered.WriteString(line.highlightedOrText())
		case diffLineRemoved:
			rendered.WriteString(out.String("-").Foreground(termenv.ANSIRed).String())
			rendered.WriteString(line.highlightedOrText())
		case diffLineContext:
			rendered.WriteString(out.String(" ").Foreground(termenv.ANSIBrightBlack).String())
			rendered.WriteString(line.highlightedOrText())
		case diffLineOldFile:
			rendered.WriteString(out.String(line.text).Foreground(termenv.ANSIRed).String())
		case diffLineNewFile:
			rendered.WriteString(out.String(line.text).Foreground(termenv.ANSIGreen).String())
		case diffLineHunk:
			rendered.WriteString(out.String(line.text).Foreground(termenv.ANSICyan).String())
		case diffLineHeader:
			rendered.WriteString(out.String(line.text).Bold().String())
		case diffLineMetadata:
			rendered.WriteString(out.String(line.text).Foreground(termenv.ANSIBrightBlack).String())
		default:
			rendered.WriteString(line.text)
		}
		rendered.WriteString(line.ending)
	}

	result := rendered.String()
	// Chroma lexers and formatters are expected to preserve token text, but a
	// third-party lexer should never be able to corrupt the patch presentation.
	if ansi.Strip(result) != patch {
		return patch
	}
	return result
}

type parsedDiff struct {
	lines []parsedDiffLine
	files []parsedDiffFile
}

type parsedDiffFile struct {
	name  string
	hunks []parsedDiffHunk
}

type parsedDiffHunk struct {
	old diffSource
	new diffSource
}

type diffSource struct {
	text strings.Builder
	refs []int
}

type parsedDiffLine struct {
	text        string
	ending      string
	kind        parsedDiffLineKind
	highlighted string
}

func (line parsedDiffLine) highlightedOrText() string {
	if line.highlighted != "" || line.text == "" {
		return line.highlighted
	}
	return line.text[1:]
}

type parsedDiffLineKind uint8

const (
	diffLinePlain parsedDiffLineKind = iota
	diffLineHeader
	diffLineMetadata
	diffLineOldFile
	diffLineNewFile
	diffLineHunk
	diffLineContext
	diffLineAdded
	diffLineRemoved
)

func parseDiff(patch string) (parsedDiff, bool) {
	lines := splitDiffLines(patch)
	parsed := parsedDiff{lines: lines}
	fileIndex := -1
	hunkIndex := -1
	seenPatch := false

	for i := 0; i < len(parsed.lines); i++ {
		line := &parsed.lines[i]

		if strings.HasPrefix(line.text, "diff --git ") {
			parsed.files = append(parsed.files, parsedDiffFile{name: diffGitPath(line.text)})
			fileIndex = len(parsed.files) - 1
			hunkIndex = -1
			line.kind = diffLineHeader
			seenPatch = true
			continue
		}

		if fileIndex >= 0 && strings.HasPrefix(line.text, "@@ ") {
			file := &parsed.files[fileIndex]
			file.hunks = append(file.hunks, parsedDiffHunk{})
			hunkIndex = len(file.hunks) - 1
			line.kind = diffLineHunk
			seenPatch = true
			continue
		}

		if fileIndex >= 0 && hunkIndex >= 0 {
			switch {
			case strings.HasPrefix(line.text, `\ No newline at end of file`):
				line.kind = diffLineMetadata
				continue
			case line.text != "":
				switch line.text[0] {
				case ' ':
					line.kind = diffLineContext
					appendDiffSourceLine(&parsed.files[fileIndex].hunks[hunkIndex].old, i, line.text[1:])
					appendDiffSourceLine(&parsed.files[fileIndex].hunks[hunkIndex].new, i, line.text[1:])
					continue
				case '+':
					line.kind = diffLineAdded
					appendDiffSourceLine(&parsed.files[fileIndex].hunks[hunkIndex].new, i, line.text[1:])
					continue
				case '-':
					line.kind = diffLineRemoved
					appendDiffSourceLine(&parsed.files[fileIndex].hunks[hunkIndex].old, i, line.text[1:])
					continue
				}
			}
			hunkIndex = -1
		}

		switch {
		case strings.HasPrefix(line.text, "--- "):
			if fileIndex < 0 {
				parsed.files = append(parsed.files, parsedDiffFile{})
				fileIndex = len(parsed.files) - 1
			}
			line.kind = diffLineOldFile
			if name := diffHeaderPath(line.text[4:]); name != "" {
				parsed.files[fileIndex].name = name
			}
			seenPatch = true
		case strings.HasPrefix(line.text, "+++ "):
			if fileIndex < 0 {
				return parsedDiff{}, false
			}
			line.kind = diffLineNewFile
			if name := diffHeaderPath(line.text[4:]); name != "" {
				parsed.files[fileIndex].name = name
			}
		case fileIndex >= 0 && isDiffMetadata(line.text):
			line.kind = diffLineMetadata
		}
	}

	return parsed, seenPatch && len(parsed.files) > 0
}

func splitDiffLines(patch string) []parsedDiffLine {
	lines := make([]parsedDiffLine, 0, strings.Count(patch, "\n")+1)
	for len(patch) > 0 {
		if newline := strings.IndexByte(patch, '\n'); newline >= 0 {
			text, ending := patch[:newline], "\n"
			if strings.HasSuffix(text, "\r") {
				text = strings.TrimSuffix(text, "\r")
				ending = "\r\n"
			}
			lines = append(lines, parsedDiffLine{text: text, ending: ending})
			patch = patch[newline+1:]
		} else {
			lines = append(lines, parsedDiffLine{text: patch})
			break
		}
	}
	return lines
}

func appendDiffSourceLine(source *diffSource, ref int, text string) {
	source.text.WriteString(text)
	source.text.WriteByte('\n')
	source.refs = append(source.refs, ref)
}

func matchDiffLexer(name string) (lexer chroma.Lexer) {
	if name == "" {
		return nil
	}
	defer func() {
		if recover() != nil {
			lexer = nil
		}
	}()
	return lexers.Match(name)
}

func highlightDiffSide(lines []parsedDiffLine, source *diffSource, lexer chroma.Lexer) {
	if len(source.refs) == 0 {
		return
	}
	highlighted, ok := highlightDiffSource(lexer, source.text.String())
	if !ok || len(highlighted) < len(source.refs) {
		return
	}
	for i, ref := range source.refs {
		candidate := highlighted[i]
		content := lines[ref].text[1:]
		if ansi.Strip(candidate) == content {
			lines[ref].highlighted = candidate
		}
	}
}

func highlightDiffSource(lexer chroma.Lexer, source string) (lines []string, ok bool) {
	defer func() {
		if recover() != nil {
			lines = nil
			ok = false
		}
	}()

	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return nil, false
	}
	var highlighted strings.Builder
	if err := formatters.TTY16.Format(&highlighted, TTYStyle(), iterator); err != nil {
		return nil, false
	}
	lines = strings.Split(highlighted.String(), "\n")
	return lines, true
}

func diffHeaderPath(field string) string {
	field = strings.TrimSpace(strings.SplitN(field, "\t", 2)[0])
	if field == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(field, `"`) {
		if unquoted, err := strconv.Unquote(field); err == nil {
			field = unquoted
		}
	}
	return strings.TrimPrefix(strings.TrimPrefix(field, "a/"), "b/")
}

func diffGitPath(header string) string {
	// Header paths are only a fallback. The ---/+++ headers, which are
	// unambiguous even when a path contains spaces, replace this value.
	fields := strings.Fields(strings.TrimPrefix(header, "diff --git "))
	if len(fields) < 2 {
		return ""
	}
	return diffHeaderPath(fields[len(fields)-1])
}

func isDiffMetadata(line string) bool {
	prefixes := [...]string{
		"index ", "old mode ", "new mode ", "new file mode ",
		"deleted file mode ", "similarity index ", "dissimilarity index ",
		"rename from ", "rename to ", "copy from ", "copy to ",
		"Binary files ", "GIT binary patch",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
