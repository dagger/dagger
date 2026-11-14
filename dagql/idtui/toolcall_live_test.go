package idtui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// liveToolCalls stands up a pretty shell frontend fed the way a live `dagger
// agent` session feeds it: spans exported as they start (a tool call's
// attributes carry its tool, not its arguments, which only arrive when it
// ends), and tool arguments streamed as JSON logs on each call's display
// span, a few bytes at a time.
type liveToolCalls struct {
	t     *testing.T
	fe    *frontendPretty
	start time.Time
}

// Span IDs of the conversation newLiveToolCalls opens.
const (
	liveRoot byte = iota + 1
	liveQuery
	liveSend
	liveAgent
	livePrompt
	liveReply
	liveTimeout
	liveNested
	liveWork // and up: the nested call's work
)

var liveTraceID = trace.TraceID{1}

// newLiveToolCalls opens a conversation as `dagger agent` runs it: the
// agent's turn beneath the passthrough query that sent it, a prompt, and the
// reply that the turn's tool calls nest under.
func newLiveToolCalls(t *testing.T, profile termenv.Profile) *liveToolCalls {
	t.Helper()
	fe := newWithTerminal(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(100, 40))
	fe.profile = profile
	fe.logs.Profile = profile
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.GCThreshold = time.Hour
	l := &liveToolCalls{t: t, fe: fe, start: time.Unix(100, 0)}
	l.span(liveRoot, 0, "dagger agent", attribute.Bool(telemetry.UIPassthroughAttr, true))
	fe.SetPrimary(prettyTestSpanID(liveRoot))
	l.span(liveQuery, liveRoot, "POST /query", attribute.Bool(telemetry.UIPassthroughAttr, true))
	l.span(liveSend, liveQuery, "Agent.send")
	l.span(liveAgent, liveSend, "agent: agent",
		attribute.Bool(telemetryattrs.AgentAttr, true),
		attribute.String(telemetryattrs.AgentIDAttr, "agent"),
		attribute.String(telemetryattrs.AgentNameAttr, "agent"))
	l.span(livePrompt, liveAgent, "LLM prompt",
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser))
	l.markdown(livePrompt, "Run it.")
	l.span(liveReply, liveAgent, "LLM response",
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant))
	l.markdown(liveReply, "Running it now.")
	l.frame()
	return l
}

// span exports a span as it starts.
func (l *liveToolCalls) span(id, parent byte, name string, attrs ...attribute.KeyValue) {
	l.t.Helper()
	stub := tracetest.SpanStub{
		Name:        name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: liveTraceID, SpanID: trace.SpanID{id}}),
		StartTime:   l.start.Add(time.Duration(id) * time.Millisecond),
		Attributes:  attrs,
	}
	if parent != 0 {
		stub.Parent = trace.NewSpanContext(trace.SpanContextConfig{TraceID: liveTraceID, SpanID: trace.SpanID{parent}})
	}
	require.NoError(l.t, l.fe.SpanExporter().ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{stub.Snapshot()}))
}

// toolCall exports a tool call's display span as displayPhases.StartToolCall
// opens it.
func (l *liveToolCalls) toolCall(id, parent byte, tool string) {
	l.span(id, parent, tool,
		attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
		attribute.Bool(telemetry.UIBoundaryAttr, true),
		attribute.Bool(telemetry.UIRollUpSpansAttr, true),
		attribute.Bool(telemetry.UIRollUpLogsAttr, true),
		attribute.String(telemetry.LLMToolAttr, tool))
}

// stream streams a display span's JSON arguments in chunks of n bytes, as a
// model writes them, rendering a frame after each.
func (l *liveToolCalls) stream(id byte, args string, n int, each func(frame string)) {
	l.t.Helper()
	for len(args) > 0 {
		chunk := args[:min(n, len(args))]
		args = args[len(chunk):]
		l.logs(frontendTestLogRecord(trace.SpanID{id}, otellog.StringValue(chunk),
			otellog.String(telemetry.ContentTypeAttr, "application/json"),
			otellog.Bool(telemetry.LogsVerboseAttr, true)))
		frame := l.frame()
		if each != nil {
			each(frame)
		}
	}
}

// closeArgs ends a display span's argument stream as displayPhases.Close
// does: a newline, then EOF.
func (l *liveToolCalls) closeArgs(id byte) {
	l.t.Helper()
	l.stream(id, "\n", 1, nil)
	l.logs(frontendTestLogRecord(trace.SpanID{id}, otellog.StringValue(""),
		otellog.String(telemetry.ContentTypeAttr, "application/json"),
		otellog.Bool(telemetry.LogsVerboseAttr, true),
		otellog.Bool(telemetry.StdioEOFAttr, true)))
}

func (l *liveToolCalls) markdown(id byte, text string) {
	l.t.Helper()
	l.logs(frontendTestLogRecord(trace.SpanID{id}, otellog.StringValue(text),
		otellog.String(telemetry.ContentTypeAttr, "text/markdown")))
}

func (l *liveToolCalls) logs(records ...sdklog.Record) {
	l.t.Helper()
	for i := range records {
		records[i].SetTraceID(liveTraceID)
	}
	require.NoError(l.t, l.fe.LogExporter().Export(context.Background(), records))
}

// frame renders a frame, as the TUI does when the dispatched exports have
// run, and returns it.
func (l *liveToolCalls) frame() string {
	return strings.Join(l.fe.tui.Step(), "\n")
}

const liveTimeoutArgs = `{"duration":"5m","tool":"DangEval","arguments":{"source":"let greeting = shout(\"hi\")\ngreeting + \"!\""}}`

// TestRunningTimeoutRendersItsCall covers a Timeout once its arguments are
// in, while it runs: they are only known from what its display span
// streamed, and its row presents as the call it wraps, snippet and all. Its
// nested call starts and works beneath it; expanded, that call's row names
// it without repeating the snippet.
func TestRunningTimeoutRendersItsCall(t *testing.T) {
	l := newLiveToolCalls(t, termenv.Ascii)
	l.toolCall(liveTimeout, liveReply, "Timeout")
	l.stream(liveTimeout, liveTimeoutArgs, len(liveTimeoutArgs), nil)
	l.closeArgs(liveTimeout)
	got := stripANSICodes(l.frame())
	require.Contains(t, got, "• DangEval timeout=5m", got)
	require.Contains(t, got, `1 │ let greeting = shout("hi")`, got)
	require.Contains(t, got, `2 │ greeting + "!"`, got)
	require.NotContains(t, got, "• Timeout", got)

	l.toolCall(liveNested, liveTimeout, "DangEval")
	l.stream(liveNested, `{"source":"let greeting = shout(\"hi\")\ngreeting + \"!\""}`, 1<<10, nil)
	l.closeArgs(liveNested)
	for id := liveWork; id < liveWork+20; id++ {
		l.span(id, liveNested, "Container.withExec")
	}
	got = stripANSICodes(l.frame())
	require.Contains(t, got, "• DangEval timeout=5m", got)
	require.Equal(t, 1, strings.Count(got, `1 │ let greeting`), got)

	l.fe.setExpanded(prettyTestSpanID(liveTimeout), true)
	l.fe.viewDirty = true
	got = stripANSICodes(l.frame())
	require.Equal(t, 2, strings.Count(got, "• DangEval"), got)
	require.Equal(t, 1, strings.Count(got, `1 │ let greeting`), got)
}

// TestRunningTimeoutBorrowsItsCallsName covers a Timeout whose nested call
// starts after its row rendered: the row picks up the call's server and bare
// name from the nested span as it arrives.
func TestRunningTimeoutBorrowsItsCallsName(t *testing.T) {
	l := newLiveToolCalls(t, termenv.Ascii)
	l.toolCall(liveTimeout, liveReply, "Timeout")
	l.stream(liveTimeout, `{"duration":"30s","tool":"editor_read","arguments":{"path":"main.go"}}`, 1<<10, nil)
	l.closeArgs(liveTimeout)
	require.Contains(t, stripANSICodes(l.frame()), "• EditorRead main.go timeout=30s")

	l.span(liveNested, liveTimeout, "editor_read",
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
		attribute.Bool(telemetry.UIBoundaryAttr, true),
		attribute.String(telemetry.LLMToolAttr, "read"),
		attribute.String(telemetry.LLMToolServerAttr, "Editor"))
	got := stripANSICodes(l.frame())
	require.Contains(t, got, "• editor Read main.go timeout=30s", got)
}

// TestStreamingToolCallRendersSoFar covers a Timeout while the model is still
// writing its arguments: from the moment it names the call it wraps, its row
// presents as that call, its source growing line by line, each line
// highlighted once it's whole.
func TestStreamingToolCallRendersSoFar(t *testing.T) {
	<-warmDangHighlighter()
	// Never hold a highlight back for being too soon after the last.
	now := time.Unix(0, 0)
	dangStreamClock = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	t.Cleanup(func() { dangStreamClock = time.Now })
	l := newLiveToolCalls(t, termenv.ANSI)
	l.toolCall(liveTimeout, liveReply, "Timeout")

	var named, partial, highlighted bool
	l.stream(liveTimeout, liveTimeoutArgs, 7, func(frame string) {
		plain := stripANSICodes(frame)
		if !strings.Contains(plain, "• DangEval") {
			// Not named yet: the Timeout itself, never a partial name.
			require.False(t, named, "stopped presenting as DangEval:\n%s", plain)
			require.Contains(t, plain, "• Timeout", plain)
			require.NotContains(t, plain, "• Dang", plain)
			return
		}
		named = true
		require.Contains(t, plain, "• DangEval timeout=5m", plain)
		if strings.Contains(plain, `2 │ greeting + "!"`) {
			// the source is whole: the document may be too
			return
		}
		if strings.Contains(plain, "1 │ let gree") && !strings.Contains(plain, "2 │") {
			partial = true
			// The line being written: plain.
			require.False(t, containsStyledLine(frame, "let gree", "\x1b[35mlet"),
				"highlighted an unfinished line:\n%s", visibleEscapes(frame))
		}
		if strings.Contains(plain, "2 │ gree") {
			// The first line is whole: highlighted, while the second,
			// still being written, is plain.
			highlighted = true
			require.True(t, containsStyledLine(frame, "let greeting", "\x1b[35mlet"),
				"didn't highlight a whole line while streaming:\n%s", visibleEscapes(frame))
			require.False(t, containsStyledLine(frame, "2 │ gree", "\x1b[35m"),
				"highlighted an unfinished line:\n%s", visibleEscapes(frame))
		}
	})
	require.True(t, named, "never presented as the call it wraps")
	require.True(t, partial, "never showed the source as it streamed")
	require.True(t, highlighted, "never showed a highlighted line while streaming")

	// Whole: all of it highlighted.
	l.closeArgs(liveTimeout)
	frame := l.frame()
	require.Contains(t, stripANSICodes(frame), `2 │ greeting + "!"`)
	require.True(t, containsStyledLine(frame, "let greeting", "\x1b[35mlet"), visibleEscapes(frame))
	require.True(t, containsStyledLine(frame, `2 │ greeting + "!"`, "\x1b[35m+"), visibleEscapes(frame))
	require.Empty(t, l.fe.dangStreams)
}
