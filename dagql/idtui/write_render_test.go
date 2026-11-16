package idtui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

func TestCodeLangForPath(t *testing.T) {
	for path, want := range map[string]string{
		"main.go":                  "Go",
		"cmd/dagger/main.go":       "Go",
		"hack/build.py":            "Python",
		".github/workflows/ci.yml": "YAML",
		"Dockerfile":               "Docker",
		// Dang highlights as Dang, with its own highlighter.
		".dagger/modules/notes/main.dang": "dang",
		"NOTES.DANG":                      "dang",
		// Nothing to highlight: plain.
		"tmp/stream-test.txt": "",
		"data.unknownext":     "",
		"":                    "",
	} {
		lang := codeLangForPath(path)
		assert.Equal(t, want, lang.name(), path)
		assert.Equal(t, want == "", lang.plain(), path)
	}
}

// goKeyword is how a Go keyword like "package" is styled: as a diff of Go
// styles it.
func goKeyword() string {
	return chromaEscape(TTYStyle(), chroma.KeywordNamespace)
}

func TestRenderCodeHighlightsByLanguage(t *testing.T) {
	source := "package main\n\n// greet\nfunc main() { println(\"hi\") }\n"
	goCode := codeLangForPath("main.go")
	styled := renderCode(termenv.ANSI, goCode, source, 0, 0)
	plain := renderCode(termenv.Ascii, goCode, source, 0, 0)
	assert.Equal(t, []string{
		`1 │ package main`,
		`2 │ `,
		`3 │ // greet`,
		`4 │ func main() { println("hi") }`,
	}, plain)
	require.Len(t, styled, len(plain))
	for i := range plain {
		assert.Equal(t, plain[i], ansi.Strip(styled[i]))
	}
	require.NotEmpty(t, goKeyword())
	assert.Contains(t, styled[0], goKeyword()+"package\x1b[0m", visibleEscapes(styled[0]))
	comment := chromaEscape(TTYStyle(), chroma.CommentSingle)
	require.NotEmpty(t, comment)
	assert.Contains(t, styled[2], comment+"// greet", visibleEscapes(styled[2]))

	// Plain text has nothing to highlight, even in color: past its gutter,
	// no escapes.
	text := renderCode(termenv.ANSI, codeLangForPath("notes.txt"), "func main() {}", 0, 0)
	require.Len(t, text, 1)
	_, code, found := strings.Cut(text[0], "│ \x1b[0m")
	require.True(t, found, visibleEscapes(text[0]))
	assert.Equal(t, "func main() {}", code)
}

// TestRenderCodeNeverChangesText is the contract renderDang's test holds
// Dang's highlighting to, for a lexer's.
func TestRenderCodeNeverChangesText(t *testing.T) {
	for _, path := range []string{"main.go", "x.py", "x.yaml", "Makefile"} {
		lang := codeLangForPath(path)
		for _, source := range []string{
			"func f(x int) int {\n\treturn x * 2\n}\n",
			`"unterminated`,
			"print(\"héllo wörld 日本語 🎉\")",
			"a\r\nb\x1b[31mc\x00\td",
			"",
		} {
			for _, width := range []int{0, 12, 40} {
				plain := renderCode(termenv.Ascii, lang, source, width, 0)
				styled := renderCode(termenv.ANSI, lang, source, width, 0)
				require.Len(t, styled, len(plain), "%s: %q at %d", path, source, width)
				for i := range plain {
					assert.Equal(t, plain[i], ansi.Strip(styled[i]), "%s: %q at %d", path, source, width)
				}
			}
		}
	}
}

// TestRenderCodeCollapsedHighlightsShownLines covers a long file collapsed:
// only the lines it shows are highlighted (and cached), not the whole file.
func TestRenderCodeCollapsedHighlightsShownLines(t *testing.T) {
	var lines []string
	for i := range 2000 {
		lines = append(lines, fmt.Sprintf("var v%d = %d", i, i))
	}
	source := "package big\n" + strings.Join(lines, "\n")
	goCode := codeLangForPath("big.go")
	got := renderCode(termenv.ANSI, goCode, source, 0, codeBodyCollapsedLines)
	require.Len(t, got, codeBodyCollapsedLines+1)
	assert.Equal(t, "   ┆ … 1985 more lines", ansi.Strip(got[codeBodyCollapsedLines]))
	codeHighlights.Lock()
	defer codeHighlights.Unlock()
	assert.NotContains(t, codeHighlights.spans, codeHighlightKey(goCode, source))
	assert.Contains(t, codeHighlights.spans, codeHighlightKey(goCode, shownLines(source, codeBodyCollapsedLines)))
}

func TestShownLines(t *testing.T) {
	assert.Equal(t, "a\nb", shownLines("a\nb\nc", 2))
	assert.Equal(t, "a\nb\nc", shownLines("a\nb\nc", 3))
	assert.Equal(t, "a\nb\nc", shownLines("a\nb\nc", 0))
	assert.Equal(t, "a", shownLines("a\n", 1))
}

func TestWriteToolCallTranscript(t *testing.T) {
	write := toolCallSnapshot("write", "filePath", "tmp/hello.go", "contents", "package main\n\nfunc main() {}\n")
	write.LLMToolServer = "editor"
	var long []string
	for i := range codeBodyCollapsedLines + 4 {
		long = append(long, fmt.Sprintf("line %d", i+1))
	}
	longWrite := toolCallSnapshot("write", "filePath", "tmp/x.txt", "contents", strings.Join(long, "\n"))
	longWrite.LLMToolServer = "editor"
	// Another tool's write, without Write's arguments: the generic header.
	other := toolCallSnapshot("write", "path", "a.txt", "contents", "hello")
	lines, _ := dangTranscript(t, 100, termenv.Ascii, write, longWrite, other)
	want := []string{
		`  • editor Write tmp/hello.go 1.0s`,
		`  1 │ package main`,
		`  2 │`,
		`  3 │ func main() {}`,
		`  • editor Write tmp/x.txt 1.0s`,
	}
	for i := range codeBodyCollapsedLines {
		want = append(want, fmt.Sprintf("  %2d │ line %d", i+1, i+1))
	}
	want = append(want, `     ┆ … 4 more lines`, `  • Write a.txt hello 1.0s`)
	assert.Equal(t, want, lines)
}

// liveWriteArgs are a Write's arguments in the order models write them, the
// order its schema offers them in: the order Write declares them in.
const liveWriteArgs = `{"filePath":"tmp/stream-test.go","contents":"package stream\n\n// Greet greets.\nfunc Greet() string {\n\treturn \"hi\"\n}\n"}`

// TestConversationReportRendersWrite covers the final report: a Write shows
// its file collapsed, as its live row does, unless the report shows tool
// calls' arguments; then, all of it.
func TestConversationReportRendersWrite(t *testing.T) {
	db := dagui.NewDB()
	toolID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	var long []string
	for i := range 40 {
		long = append(long, fmt.Sprintf("line %d", i+1))
	}
	db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID: toolID, TraceID: prettyTestTraceID(), Name: "editor_write",
		LLMRole: "assistant", LLMTool: "write", LLMToolServer: "editor",
		LLMToolArgNames:  []string{"filePath", "contents"},
		LLMToolArgValues: []string{"notes.dang", strings.Join(long, "\n")},
		StartTime:        start, EndTime: start.Add(time.Second), Final: true,
	}})
	fe := NewWithDB(io.Discard, db)
	fe.profile = termenv.Ascii
	report := func() string {
		var buf strings.Builder
		fe.renderMessageLogs(NewOutput(&buf, termenv.WithProfile(termenv.Ascii)), db.Spans.Map[toolID], 0)
		return buf.String()
	}
	got := report()
	assert.Contains(t, got, "16 │ line 16\n")
	assert.NotContains(t, got, "line 17")
	assert.Contains(t, got, "┆ … 24 more lines")

	fe.FrontendOpts.Verbosity = toolArgsVerbosity
	got = report()
	assert.Contains(t, got, "40 │ line 40\n")
	assert.NotContains(t, got, "more lines")
	assert.NotContains(t, got, `{"filePath"`)
}

// TestStreamingWriteRendersItsContents covers a Write while the model writes
// its arguments, at a verbosity that shows tool calls' JSON arguments: its
// header names the file once its path is whole, and its contents grow
// beneath it as code, each line highlighted once it's whole, in place of
// its JSON arguments. Once it ends, its result shows beneath them, and its
// header and body are what they were.
func TestStreamingWriteRendersItsContents(t *testing.T) {
	// Never hold a highlight back for being too soon after the last.
	now := time.Unix(0, 0)
	codeStreamClock = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	t.Cleanup(func() { codeStreamClock = time.Now })
	l := newLiveToolCalls(t, termenv.ANSI)
	l.fe.FrontendOpts.Verbosity = toolArgsVerbosity
	headerArgs := []string{"filePath", "contents", "offset", "limit", "args"}
	attrs := l.moduleToolCall(liveTimeout, liveReply, "editor", "write", headerArgs...)

	var named, partial, highlighted bool
	l.stream(liveTimeout, liveWriteArgs, 6, func(frame string) {
		plain := stripANSICodes(frame)
		require.NotContains(t, plain, `{"filePath"`, "JSON arguments shown:\n%s", plain)
		header := lineWith(plain, "editor Write")
		require.NotEmpty(t, header, plain)
		if !strings.Contains(header, "tmp/stream-test.go") {
			require.False(t, named, "stopped naming the file:\n%s", plain)
			require.NotContains(t, header, "tmp/", "a partial path:\n%s", plain)
			return
		}
		named = true
		if strings.Contains(plain, "1 │ package str") && !strings.Contains(plain, "2 │") {
			partial = true
			require.False(t, containsStyledLine(frame, "package str", goKeyword()+"package"),
				"highlighted an unfinished line:\n%s", visibleEscapes(frame))
		}
		if strings.Contains(plain, "4 │ func Gr") && !strings.Contains(plain, "5 │") {
			highlighted = true
			require.True(t, containsStyledLine(frame, "package stream", goKeyword()+"package"),
				"didn't highlight a whole line while streaming:\n%s", visibleEscapes(frame))
			require.False(t, containsStyledLine(frame, "4 │ func Gr", goKeyword()),
				"highlighted an unfinished line:\n%s", visibleEscapes(frame))
		}
	})
	require.True(t, named, "never named the file")
	require.True(t, partial, "never showed the contents as they streamed")
	require.True(t, highlighted, "never showed a highlighted line while streaming")
	l.closeArgs(liveTimeout)

	streamed := stripANSICodes(l.frame())
	require.Contains(t, streamed, "• editor Write tmp/stream-test.go", streamed)
	require.Contains(t, streamed, `5 │   return "hi"`, streamed)

	// It runs, and ends with its result and the attributes the engine
	// records.
	l.logs(frontendTestLogRecord(trace.SpanID{liveTimeout}, otellog.StringValue("tmp/stream-test.go +6\n")))
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(liveWriteArgs), &args))
	names, values := dagui.ToolHeaderArgs(headerArgs, args)
	l.endToolCall(liveTimeout, liveReply, "editor_write", attrs, names, values)
	frame := l.frame()
	got := stripANSICodes(frame)
	require.Contains(t, got, "• editor Write tmp/stream-test.go", got)
	require.Contains(t, lineWith(got, "editor Write"), "42.0s", got)
	require.Contains(t, got, "6 │ }", got)
	require.Contains(t, got, "tmp/stream-test.go +6", got)
	require.Less(t, strings.Index(got, "6 │ }"), strings.Index(got, "tmp/stream-test.go +6"),
		"result beneath the body:\n%s", got)
	require.True(t, containsStyledLine(frame, "package stream", goKeyword()+"package"), visibleEscapes(frame))
	require.NotContains(t, got, `{"filePath"`, got)
	require.Empty(t, l.fe.codeStreams)
}
