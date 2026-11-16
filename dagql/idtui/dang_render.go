package idtui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/dang/v2/pkg/dang"

	"github.com/dagger/dagger/dagql/dagui"
)

// Tool calls that render from their full arguments (see dagui.ToolCallView):
// the Dang builtins show their snippet as code, Write the file it writes,
// and Timeout presents as the call it wraps.
//
//	• DangEval notes timeout=2m 1.2s
//	  1 │ let greeting = shout("hi")
//	  2 │ greeting + "!"
//	  ┆ String! = "HI!"
//
//	• editor Write hello.go 0.1s
//	  1 │ package main
//	  2 │
//	  3 │ func main() {}
//	  ┆ hello.go +3

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
	file, isWrite := view.WriteFile()
	if !isDang && !isWrite && !view.Wrapped() {
		// A Timeout whose wrapped tool isn't known yet, or a write that
		// isn't Write's.
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
	case isWrite:
		// Its contents are its body (see toolCodeBody).
		if path := dagui.SanitizeInline(firstLineOf(file.Path)); path != "" {
			renderToolArgParts(out, []dagui.ToolArgPart{{Text: path, Style: dagui.ToolArgPath}})
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

// codeBodyCollapsedLines is how many lines of a code body a collapsed tool
// call shows; expanding the call shows the rest.
const codeBodyCollapsedLines = 16

// codeBody is code a tool call shows as its body: a Dang snippet's source,
// or the contents Write writes (file), and how it's highlighted.
type codeBody struct {
	source string
	lang   codeLang
	file   bool
}

// toolCodeBody returns the code a tool-call span shows as its body, in place
// of its JSON arguments, if it is a call that has one -- even before any of
// it has arrived -- and whether it's still streaming in.
func (fe *frontendPretty) toolCodeBody(span *dagui.Span) (body codeBody, streaming, ok bool) {
	if !isToolCallDisplay(span) || !dagui.RendersToolArgs(span.LLMTool) {
		return codeBody{}, false, false
	}
	view := fe.db.ToolCallView(span)
	if snippet, isDang := view.DangSnippet(); isDang {
		body = codeBody{source: snippet.Source, lang: dangCode}
	} else if file, isWrite := view.WriteFile(); isWrite {
		body = codeBody{source: file.Contents, lang: codeLangForPath(file.Path), file: true}
	} else {
		return codeBody{}, false, false
	}
	return body, view.Streaming, true
}

// hasToolBody reports whether a tool-call span shows a code body (see
// toolCodeBody) in place of its JSON arguments.
func (fe *frontendPretty) hasToolBody(span *dagui.Span) bool {
	_, _, ok := fe.toolCodeBody(span)
	return ok
}

// toolBodyRows renders a tool-call row's code body (toolBodyLines) beneath
// its title, indented to line up with the call, and sized to the width the
// row's logs wrap to. A collapsed row shows the first codeBodyCollapsedLines
// lines.
func (fe *frontendPretty) toolBodyRows(r *renderer, row *dagui.TraceRow) []string {
	if !isToolCallDisplay(row.Span) || !dagui.RendersToolArgs(row.Span.LLMTool) {
		return nil
	}
	if parent := row.Span.ParentSpan; parent != nil && fe.db.WrappedCallSpan(parent) == row.Span {
		// The nested call of a Timeout, shown when it's expanded: the
		// Timeout's row already shows its body.
		return nil
	}
	indent := fe.toolBlockIndent(r, row)
	width := 0
	if fe.contentWidth > 0 {
		width = max(fe.contentWidth-ansi.StringWidth(indent), 1)
	}
	maxLines := codeBodyCollapsedLines
	if row.Expanded {
		maxLines = 0
	}
	// The final render is never redrawn: it waits for the highlighter.
	lines := fe.toolBodyLines(row.Span, width, maxLines, fe.finalRender)
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

// toolBodyLines renders the code body of a tool-call span, if it has one
// (see toolCodeBody): a small editor view, with line numbers in a gutter and
// the code highlighted, its lines wrapped to width (0: unwrapped). It shows
// at most maxLines lines (0: all), then how many are left. The lines carry
// no row indent.
//
// Until Dang's highlighter is ready (see dangHighlighter), Dang renders
// plain, and the view re-renders once it is -- unless wait is set, for a
// one-shot render that would never be redrawn.
//
// Code still streaming in highlights as it grows, line by line (see
// streamingHighlight): the line being written renders plain until it's
// done.
func (fe *frontendPretty) toolBodyLines(span *dagui.Span, width, maxLines int, wait bool) []string {
	body, streaming, ok := fe.toolCodeBody(span)
	if !ok || strings.TrimSpace(body.source) == "" {
		return nil
	}
	// codeStreams belongs to the render loop: a one-shot render (wait)
	// leaves it be, and highlights what it has as if it were whole.
	if !streaming && !wait {
		delete(fe.codeStreams, span.ID)
	}
	streaming = streaming && !wait
	source := normalizeSource(body.source)
	var spans []dang.HighlightSpan
	switch {
	case fe.profile == termenv.Ascii || body.lang.plain():
	case body.lang.dang && !fe.dangHighlighterReady(wait):
	case streaming:
		spans = fe.streamingHighlight(span.ID, body.lang, source, maxLines)
	default:
		spans = highlightCode(body.lang, shownLines(source, maxLines))
	}
	return renderCodeSpans(fe.profile, body.lang, spans, source, width, maxLines)
}

// codeStreamHighlight is a streaming code body's highlight (see
// streamingHighlight): the language and prefix of its source last
// highlighted, its spans, and when that was and how long it took.
type codeStreamHighlight struct {
	lang   string
	source string
	spans  []dang.HighlightSpan
	at     time.Time
	cost   time.Duration
}

// Highlighting a streaming body runs its highlighter on the render loop --
// Dang's takes about 15µs a line (3ms for 200 lines, 30ms for 2000) -- so
// it's held to at most once per codeStreamHighlightInterval, and at most a
// quarter of the time since the last one.
const codeStreamHighlightInterval = 100 * time.Millisecond

// maxCodeStreams bounds frontendPretty.codeStreams, against bodies that
// stopped streaming without rendering again; it's cleared when full.
const maxCodeStreams = 32

// codeStreamClock is the clock streamingHighlight throttles by; tests stand
// in for it.
var codeStreamClock = time.Now

// streamingHighlight highlights a streaming body's (normalized) source as it
// grows: the lines of it that are whole, and that a row showing maxLines
// lines (0: all) shows, re-highlighting when a line completes -- at most so
// often (see codeStreamHighlightInterval). In between, it returns the last
// highlight, which covers a prefix of source: what came after renders plain
// until the next. Each span keeps its own highlight, rather than filling
// codeHighlights with every prefix.
func (fe *frontendPretty) streamingHighlight(id dagui.SpanID, lang codeLang, source string, maxLines int) []dang.HighlightSpan {
	whole := wholeLines(source, maxLines)
	if whole == "" {
		return nil
	}
	last := fe.codeStreams[id]
	if last != nil && (last.lang != lang.name() || !strings.HasPrefix(source, last.source)) {
		// The source changed under it, rather than grew: start over, but
		// keep to the schedule.
		last = &codeStreamHighlight{lang: lang.name(), at: last.at, cost: last.cost}
		fe.codeStreams[id] = last
	}
	if last != nil && len(whole) <= len(last.source) {
		return last.spans
	}
	now := codeStreamClock()
	if last != nil && now.Sub(last.at) < max(codeStreamHighlightInterval, 4*last.cost) {
		return last.spans
	}
	if fe.codeStreams == nil || len(fe.codeStreams) >= maxCodeStreams {
		fe.codeStreams = map[dagui.SpanID]*codeStreamHighlight{}
	}
	start := time.Now()
	spans := lang.highlight(whole)
	fe.codeStreams[id] = &codeStreamHighlight{
		lang:   lang.name(),
		source: whole,
		spans:  spans,
		at:     now,
		cost:   time.Since(start),
	}
	return spans
}

// wholeLines returns the prefix of source its whole lines make up -- the
// ones a newline ends -- up to maxLines of them (0: all).
func wholeLines(source string, maxLines int) string {
	end, lines := 0, 0
	for i := 0; i < len(source); i++ {
		if source[i] != '\n' {
			continue
		}
		end, lines = i, lines+1
		if maxLines > 0 && lines == maxLines {
			break
		}
	}
	return source[:end]
}

// shownLines returns the prefix of source a view showing maxLines lines (0:
// all) shows: its first maxLines lines, the last of them whether or not a
// newline ends it.
func shownLines(source string, maxLines int) string {
	if maxLines <= 0 {
		return source
	}
	end := 0
	for range maxLines {
		i := strings.IndexByte(source[end:], '\n')
		if i < 0 {
			return source
		}
		end += i + 1
	}
	return source[:end-1]
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

// renderCode renders source in lang as toolBodyLines describes, highlighted
// unless the profile has no color.
func renderCode(profile termenv.Profile, lang codeLang, source string, width, maxLines int) []string {
	source = normalizeSource(source)
	var spans []dang.HighlightSpan
	if profile != termenv.Ascii {
		spans = highlightCode(lang, shownLines(source, maxLines))
	}
	return renderCodeSpans(profile, lang, spans, source, width, maxLines)
}

// normalizeSource returns source with Windows line endings made Unix ones:
// the form it's highlighted and rendered in.
func normalizeSource(source string) string {
	return strings.ReplaceAll(source, "\r\n", "\n")
}

// renderCodeSpans renders normalized source in lang, styled by spans (its
// highlighter's, of source or a prefix of it).
func renderCodeSpans(profile termenv.Profile, lang codeLang, spans []dang.HighlightSpan, source string, width, maxLines int) []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))
	text := strings.TrimRight(source, "\n")
	// Only the lines shown are split up and styled: a written file can
	// run to thousands of lines, of which a collapsed row shows a few.
	total := strings.Count(text, "\n") + 1
	shown := text
	if maxLines > 0 && total > maxLines {
		shown = shownLines(text, maxLines)
	}
	lines := styledLines(spans, shown)

	numWidth := len(strconv.Itoa(len(lines)))
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
	for i, line := range lines {
		for j, chunk := range wrapStyledRunes(line, codeWidth) {
			num := 0
			if j == 0 {
				num = i + 1
			}
			rendered = append(rendered, gutter(num)+chunk.render(out, lang))
		}
	}
	if hidden := total - len(lines); hidden > 0 {
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

// render renders the runes, styling each run of one class as lang does.
func (runes styledRunes) render(out TermOutput, lang codeLang) string {
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
		b.WriteString(lang.style(out, runes[i].class, string(text)))
		i = j
	}
	return b.String()
}

// styledLines splits source into lines of classified runes: tabs become two
// spaces and other control characters are dropped, so a line's width is
// what the terminal shows. Classification is best-effort, by spans (rune
// offsets into source): source they don't cover is unclassified.
func styledLines(spans []dang.HighlightSpan, source string) []styledRunes {
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

// codeLang is how a code body is highlighted: as Dang, by Dang's own
// highlighter; by a chroma lexer, as diffs are (see highlightDiff); or not
// at all.
type codeLang struct {
	dang  bool
	lexer chroma.Lexer
}

// dangCode is Dang's codeLang.
var dangCode = codeLang{dang: true}

// codeLangForPath returns the codeLang of a file at path, by its name: Dang
// for a .dang file, else the lexer a diff of it highlights with, else none.
func codeLangForPath(path string) codeLang {
	if strings.EqualFold(filepath.Ext(path), ".dang") {
		return dangCode
	}
	return codeLang{lexer: codeLexer(path)}
}

func (lang codeLang) plain() bool {
	return !lang.dang && lang.lexer == nil
}

// name identifies the language, for caching its highlights.
func (lang codeLang) name() string {
	switch {
	case lang.dang:
		return "dang"
	case lang.lexer != nil:
		return lang.lexer.Config().Name
	}
	return ""
}

// highlight classifies source, or returns nil if it can't. Dang's classes
// are its token classes (see styleDangClass); a lexer's are the escape
// sequences its tokens are styled with (see chromaEscape).
func (lang codeLang) highlight(source string) []dang.HighlightSpan {
	switch {
	case lang.dang:
		return highlightDangUncached(source)
	case lang.lexer != nil:
		return highlightChroma(lang.lexer, source)
	}
	return nil
}

// style styles text of a class its highlight gave it.
func (lang codeLang) style(out TermOutput, class, text string) string {
	if lang.dang {
		return styleDangClass(out, class, text)
	}
	if class == "" {
		return text
	}
	return class + text + "\x1b[0m"
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

// codeLexers caches codeLexer's lexers by file name: matching one tries
// every lexer's patterns, and a row re-renders on every update.
var codeLexers = struct {
	sync.Mutex
	lexers map[string]chroma.Lexer
}{}

// maxCodeLexers bounds codeLexers; it is cleared when full.
const maxCodeLexers = 256

// codeLexer returns the chroma lexer for a file at path, as diffs match one
// (matchDiffLexer), or nil for none -- or plain text, which has nothing to
// highlight.
func codeLexer(path string) chroma.Lexer {
	name := filepath.Base(path)
	codeLexers.Lock()
	defer codeLexers.Unlock()
	if lexer, ok := codeLexers.lexers[name]; ok {
		return lexer
	}
	lexer := matchDiffLexer(name)
	if lexer != nil && lexer.Config().Name == "plaintext" {
		lexer = nil
	}
	if lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}
	if codeLexers.lexers == nil || len(codeLexers.lexers) >= maxCodeLexers {
		codeLexers.lexers = map[string]chroma.Lexer{}
	}
	codeLexers.lexers[name] = lexer
	return lexer
}

// highlightChroma classifies source with lexer, by the escape sequence each
// token renders with in the TTY style diffs use (see highlightDiffSource).
func highlightChroma(lexer chroma.Lexer, source string) (spans []dang.HighlightSpan) {
	defer func() {
		if recover() != nil {
			spans = nil
		}
	}()
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return nil
	}
	style := TTYStyle()
	pos := 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		n := utf8.RuneCountInString(token.Value)
		if esc := chromaEscape(style, token.Type); esc != "" && n > 0 {
			if last := len(spans) - 1; last >= 0 && spans[last].End == pos && spans[last].Class == esc {
				spans[last].End += n
			} else {
				spans = append(spans, dang.HighlightSpan{Start: pos, End: pos + n, Class: esc})
			}
		}
		pos += n
	}
	return spans
}

type chromaEscapeKey struct {
	style *chroma.Style
	token chroma.TokenType
}

// chromaEscapes caches chromaEscape's escape sequences.
var chromaEscapes sync.Map // chromaEscapeKey -> string

// chromaEscape returns the escape sequence the TTY16 formatter, as diffs use
// it, styles a token of type token with in style; "" for none.
func chromaEscape(style *chroma.Style, token chroma.TokenType) string {
	key := chromaEscapeKey{style, token}
	if esc, ok := chromaEscapes.Load(key); ok {
		return esc.(string)
	}
	esc := ""
	var b strings.Builder
	if err := formatters.TTY16.Format(&b, style, chroma.Literator(chroma.Token{Type: token, Value: "x"})); err == nil {
		if before, _, found := strings.Cut(b.String(), "x"); found {
			esc = before
		}
	}
	chromaEscapes.Store(key, esc)
	return esc
}

// codeHighlights caches highlighted sources: a row re-renders on every
// update to its span, and the source it shows rarely changes.
var codeHighlights = struct {
	sync.Mutex
	spans map[string][]dang.HighlightSpan
	bytes int
}{}

// codeHighlights is cleared when it holds maxCodeHighlights sources, or
// maxCodeHighlightBytes of them.
const (
	maxCodeHighlights     = 256
	maxCodeHighlightBytes = 4 << 20
)

func codeHighlightKey(lang codeLang, source string) string {
	return lang.name() + "\x00" + source
}

// highlightCode classifies source in lang, or returns nil if it can't.
func highlightCode(lang codeLang, source string) (spans []dang.HighlightSpan) {
	if lang.plain() {
		return nil
	}
	key := codeHighlightKey(lang, source)
	codeHighlights.Lock()
	defer codeHighlights.Unlock()
	if cached, ok := codeHighlights.spans[key]; ok {
		return cached
	}
	defer func() {
		if len(codeHighlights.spans) >= maxCodeHighlights || codeHighlights.bytes+len(key) > maxCodeHighlightBytes {
			codeHighlights.spans, codeHighlights.bytes = nil, 0
		}
		if codeHighlights.spans == nil {
			codeHighlights.spans = map[string][]dang.HighlightSpan{}
		}
		codeHighlights.spans[key] = spans
		codeHighlights.bytes += len(key)
	}()
	return lang.highlight(source)
}

// highlightDangUncached classifies source with Dang's own highlighter (its
// tree-sitter grammar, run as WebAssembly), or returns nil if it can't.
func highlightDangUncached(source string) (spans []dang.HighlightSpan) {
	defer func() {
		if recover() != nil {
			spans = nil
		}
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
