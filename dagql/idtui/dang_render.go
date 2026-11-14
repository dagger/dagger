package idtui

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/dang/v2/pkg/dang"

	"github.com/dagger/dagger/dagql/dagui"
)

// Tool calls that render from their full arguments (see dagui.ToolCallView):
// the Dang builtins show their snippet as code, and Timeout presents as the
// call it wraps.
//
//	• DangEval notes timeout=2m 1.2s
//	  1 │ let greeting = shout("hi")
//	  2 │ greeting + "!"
//	  ┆ String! = "HI!"

// renderToolCallTitle renders a tool-call span's header: its tool and a
// summary of its arguments. It returns false for a call that presents as
// itself with nothing special to show, leaving the generic header to the
// caller.
func (r *renderer) renderToolCallTitle(out TermOutput, span *dagui.Span) bool {
	if !dagui.RendersToolArgs(span.LLMTool) {
		return false
	}
	view := r.db.ToolCallView(span)
	snippet, isDang := view.DangSnippet()
	if !isDang && !view.Wrapped() {
		// A Timeout whose wrapped tool isn't known yet.
		return false
	}
	server, name := span.LLMToolServer, view.Tool
	if view.Wrapped() {
		// The model names the wrapped tool as it sees it, e.g.
		// "engineLab_engineTest"; once the call runs, its own span knows
		// the tool's server and bare name.
		server = ""
		if wrapped := r.db.WrappedCallSpan(span); wrapped != nil {
			server, name = wrapped.LLMToolServer, wrapped.LLMTool
		}
	}
	if server != "" {
		fmt.Fprint(out, out.String(dagui.ToolServerLabel(server)).Foreground(termenv.ANSIBrightMagenta))
		fmt.Fprint(out, " ")
	}
	fmt.Fprint(out, out.String(dagui.ToolNameLabel(name)).Bold())
	switch {
	case isDang:
		if at := dagui.SanitizeInline(firstLineOf(snippet.At)); at != "" {
			fmt.Fprint(out, " ", out.String(at).Foreground(termenv.ANSICyan))
		}
		if search := dagui.SanitizeInline(firstLineOf(snippet.Search)); search != "" {
			fmt.Fprint(out, " ", out.String("search "+strconv.Quote(search)).Faint())
		}
	default:
		if !renderToolArgParts(out, view.ArgsSummary()) {
			if fallback := view.ArgsFallback(); fallback != "" {
				fmt.Fprint(out, "(", fallback, ")")
			}
		}
	}
	if view.Wrapped() {
		fmt.Fprint(out, " ", out.String("timeout="+dagui.SanitizeInline(view.Timeout)).Faint())
	}
	return true
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// dangSourceCollapsedLines is how many lines of a Dang snippet a collapsed
// tool call shows; expanding the call shows the rest.
const dangSourceCollapsedLines = 16

// dangSourceRows renders a tool-call row's Dang source (dangSourceLines)
// beneath its title, indented to line up with the call, and sized to the
// width the row's logs wrap to. A collapsed row shows the first
// dangSourceCollapsedLines lines.
func (fe *frontendPretty) dangSourceRows(r *renderer, row *dagui.TraceRow) []string {
	if !isToolCallDisplay(row.Span) || !dagui.RendersToolArgs(row.Span.LLMTool) {
		return nil
	}
	if parent := row.Span.ParentSpan; parent != nil && fe.db.WrappedCallSpan(parent) == row.Span {
		// The nested call of a Timeout, shown when it's expanded: the
		// Timeout's row already shows its snippet.
		return nil
	}
	indent := fe.toolBlockIndent(r, row)
	width := 0
	if fe.contentWidth > 0 {
		width = max(fe.contentWidth-ansi.StringWidth(indent), 1)
	}
	maxLines := dangSourceCollapsedLines
	if row.Expanded {
		maxLines = 0
	}
	// The final render is never redrawn: it waits for the highlighter.
	lines := fe.dangSourceLines(row.Span, width, maxLines, fe.finalRender)
	for i, line := range lines {
		lines[i] = indent + line
	}
	return lines
}

// toolBlockIndent is the indent of a block beneath a tool-call row's title:
// logLinePrefixes' log prefix, without its gutter.
func (fe *frontendPretty) toolBlockIndent(r *renderer, row *dagui.TraceRow) string {
	if row.Depth == -1 {
		// zoomed: flush
		return ""
	}
	buf := new(strings.Builder)
	indentOut := NewOutput(buf, termenv.WithProfile(fe.profile))
	r.fancyIndent(indentOut, row, false, false)
	if fe.shellToolRow(row) {
		fmt.Fprint(indentOut, "  ")
	}
	return buf.String()
}

// dangSourceLines renders the Dang source a tool-call span runs or checks, if
// it is one that does: a small editor view, with line numbers in a gutter and
// the code highlighted, its lines wrapped to width (0: unwrapped). It shows
// at most maxLines source lines (0: all), then how many are left. The lines
// carry no row indent.
//
// Until Dang's highlighter is ready (see dangHighlighter), the code renders
// plain, and the view re-renders once it is -- unless wait is set, for a
// one-shot render that would never be redrawn.
func (fe *frontendPretty) dangSourceLines(span *dagui.Span, width, maxLines int, wait bool) []string {
	if !isToolCallDisplay(span) || !dagui.RendersToolArgs(span.LLMTool) {
		return nil
	}
	snippet, ok := fe.db.ToolCallView(span).DangSnippet()
	if !ok || strings.TrimSpace(snippet.Source) == "" {
		return nil
	}
	highlight := fe.profile != termenv.Ascii && fe.dangHighlighterReady(wait)
	return renderDangSourceWith(fe.profile, highlight, snippet.Source, width, maxLines)
}

// dangHighlighterReady reports whether Dang's highlighter is ready, warming
// it if it hasn't been; with wait, it waits for it. A frontend that rendered
// without it re-renders its rows once it is ready.
func (fe *frontendPretty) dangHighlighterReady(wait bool) bool {
	ready := dangHighlighterWarmup()
	if wait {
		<-ready
		return true
	}
	select {
	case <-ready:
		return true
	default:
	}
	if !fe.dangHighlightPending {
		fe.dangHighlightPending = true
		go func() {
			<-ready
			fe.dispatch(func() {
				for _, tree := range fe.spanTrees {
					tree.Update()
				}
				for view := range fe.spanLists {
					view.UpdateAll()
				}
				fe.testsDirty = true
				fe.viewDirty = true
				fe.Update()
			})
		}()
	}
	return false
}

// renderDangSource renders Dang source as dangSourceLines describes,
// highlighted unless the profile has no color.
func renderDangSource(profile termenv.Profile, source string, width, maxLines int) []string {
	return renderDangSourceWith(profile, profile != termenv.Ascii, source, width, maxLines)
}

func renderDangSourceWith(profile termenv.Profile, highlight bool, source string, width, maxLines int) []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))
	source = strings.TrimRight(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	lines := styledDangLines(highlight, source)

	shown := lines
	if maxLines > 0 && len(lines) > maxLines {
		shown = lines[:maxLines]
	}
	numWidth := len(strconv.Itoa(len(shown)))
	gutter := func(num int) string {
		label := strings.Repeat(" ", numWidth)
		if num > 0 {
			label = fmt.Sprintf("%*d", numWidth, num)
		}
		return out.String(label + " │ ").Foreground(termenv.ANSIBrightBlack).Faint().String()
	}
	codeWidth := 0
	if width > 0 {
		// Never squeeze code narrower than this: a narrower terminal clips
		// rather than wrapping every few characters.
		codeWidth = max(width-numWidth-3, 8)
	}

	var rendered []string
	for i, line := range shown {
		for j, chunk := range wrapStyledRunes(line, codeWidth) {
			num := 0
			if j == 0 {
				num = i + 1
			}
			rendered = append(rendered, gutter(num)+chunk.render(out))
		}
	}
	if hidden := len(lines) - len(shown); hidden > 0 {
		noun := "lines"
		if hidden == 1 {
			noun = "line"
		}
		rendered = append(rendered, out.String(fmt.Sprintf("%s ┆ … %d more %s", strings.Repeat(" ", numWidth), hidden, noun)).
			Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	return rendered
}

// styledRune is one display cell's worth of source: a rune, and the token
// class it highlights as ("" for none).
type styledRune struct {
	r     rune
	class string
}

type styledRunes []styledRune

// render renders the runes, styling each run of one class.
func (runes styledRunes) render(out TermOutput) string {
	var b strings.Builder
	for i := 0; i < len(runes); {
		j := i
		for j < len(runes) && runes[j].class == runes[i].class {
			j++
		}
		text := make([]rune, 0, j-i)
		for _, sr := range runes[i:j] {
			text = append(text, sr.r)
		}
		b.WriteString(styleDangClass(out, runes[i].class, string(text)))
		i = j
	}
	return b.String()
}

// styledDangLines splits source into lines of classified runes: tabs become
// two spaces and other control characters are dropped, so a line's width is
// what the terminal shows. Classification is best-effort: source that
// doesn't highlight (or isn't asked to) is all unclassified.
func styledDangLines(highlight bool, source string) []styledRunes {
	var spans []dang.HighlightSpan
	if highlight {
		spans = highlightDang(source)
	}
	runes := []rune(source)
	classes := make([]string, len(runes))
	for _, span := range spans {
		for i := max(span.Start, 0); i < span.End && i < len(classes); i++ {
			classes[i] = span.Class
		}
	}
	lines := []styledRunes{nil}
	for i, r := range runes {
		cur := &lines[len(lines)-1]
		switch {
		case r == '\n':
			lines = append(lines, nil)
		case r == '\t':
			*cur = append(*cur, styledRune{' ', classes[i]}, styledRune{' ', classes[i]})
		case r < 0x20 || r == 0x7f:
			// a stray CR or escape: never write it to the terminal
		default:
			*cur = append(*cur, styledRune{r, classes[i]})
		}
	}
	return lines
}

// wrapStyledRunes splits a line into chunks of at most width cells (0: one
// chunk). An empty line is one empty chunk.
func wrapStyledRunes(line styledRunes, width int) []styledRunes {
	if width <= 0 {
		return []styledRunes{line}
	}
	var chunks []styledRunes
	var cur styledRunes
	curWidth := 0
	for _, sr := range line {
		w := ansi.StringWidth(string(sr.r))
		if curWidth+w > width && len(cur) > 0 {
			chunks = append(chunks, cur)
			cur, curWidth = nil, 0
		}
		cur = append(cur, sr)
		curWidth += w
	}
	return append(chunks, cur)
}

// styleDangClass styles text of a Dang token class, in the palette the Dang
// REPL uses: the terminal's own 16 colors, so the theme follows it.
func styleDangClass(out TermOutput, class, text string) string {
	style := out.String(text)
	switch class {
	case dang.ClassKeyword, dang.ClassOperator:
		style = style.Foreground(termenv.ANSIMagenta)
	case dang.ClassType, dang.ClassNumber:
		style = style.Foreground(termenv.ANSIYellow)
	case dang.ClassString:
		style = style.Foreground(termenv.ANSIGreen)
	case dang.ClassEscape, dang.ClassBuiltin:
		style = style.Foreground(termenv.ANSICyan)
	case dang.ClassComment, dang.ClassDirective:
		style = style.Foreground(termenv.ANSIBrightBlack)
	case dang.ClassFunction:
		style = style.Foreground(termenv.ANSIBlue)
	case dang.ClassSelf, dang.ClassProperty, dang.ClassLabel:
		style = style.Foreground(termenv.ANSIRed)
	default:
		return text
	}
	return style.String()
}

// dangHighlights caches highlighted sources: a row re-renders on every
// update to its span, and the source it shows rarely changes.
var dangHighlights = struct {
	sync.Mutex
	spans map[string][]dang.HighlightSpan
}{}

// maxDangHighlights bounds dangHighlights; it is cleared when full.
const maxDangHighlights = 256

// highlightDang classifies source with Dang's own highlighter (its
// tree-sitter grammar, run as WebAssembly), or returns nil if it can't.
func highlightDang(source string) (spans []dang.HighlightSpan) {
	dangHighlights.Lock()
	defer dangHighlights.Unlock()
	if cached, ok := dangHighlights.spans[source]; ok {
		return cached
	}
	defer func() {
		if recover() != nil {
			spans = nil
		}
		if len(dangHighlights.spans) >= maxDangHighlights {
			dangHighlights.spans = nil
		}
		if dangHighlights.spans == nil {
			dangHighlights.spans = map[string][]dang.HighlightSpan{}
		}
		dangHighlights.spans[source] = spans
	}()
	return dang.Highlight(source)
}

// dangHighlighter is Dang's highlighter warming up: its first use compiles
// the tree-sitter grammar's WebAssembly module, which takes a moment (around
// half a second with a cold cache), and a frontend must not stall a render
// on it. ready is closed once it's done, whether or not it worked.
var dangHighlighter struct {
	once  sync.Once
	ready chan struct{}
}

// dangHighlighterWarmup is how frontends warm Dang's highlighter; tests
// stand in for it to hold it cold.
var dangHighlighterWarmup = warmDangHighlighter

// warmDangHighlighter starts warming Dang's highlighter, if nothing has yet,
// and returns a channel closed once it's ready.
func warmDangHighlighter() <-chan struct{} {
	dangHighlighter.once.Do(func() {
		dangHighlighter.ready = make(chan struct{})
		go func() {
			defer close(dangHighlighter.ready)
			defer func() { _ = recover() }()
			dang.Highlight("let warm = 1")
		}()
	})
	return dangHighlighter.ready
}
