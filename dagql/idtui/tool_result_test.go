package idtui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// failToolCall ends a tool call's display span as endToolCallDisplay does
// for a call that errored.
func (l *liveToolCalls) failToolCall(id, parent byte, name string, attrs []attribute.KeyValue, message string) {
	l.t.Helper()
	stub := tracetest.SpanStub{
		Name:        name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: liveTraceID, SpanID: trace.SpanID{id}}),
		Parent:      trace.NewSpanContext(trace.SpanContextConfig{TraceID: liveTraceID, SpanID: trace.SpanID{parent}}),
		StartTime:   l.start.Add(time.Duration(id) * time.Millisecond),
		EndTime:     l.start.Add(time.Duration(id)*time.Millisecond + 42*time.Second),
		Attributes:  attrs,
		Status:      sdktrace.Status{Code: codes.Error, Description: message},
	}
	require.NoError(l.t, l.fe.SpanExporter().ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{stub.Snapshot()}))
}

// TestToolArgsShowAtHighVerbosity covers a tool call's raw JSON arguments:
// at toolArgsVerbosity every call shows them, those rendered richer too --
// beneath their code body -- and at the default verbosity none does.
func TestToolArgsShowAtHighVerbosity(t *testing.T) {
	for _, c := range []struct {
		name, tool, args, body string
	}{
		{"dang eval", "DangEval", `{"source":"1 + 1"}`, "1 │ 1 + 1"},
		{"dang check", "DangCheck", `{"source":"ws"}`, ""},
		{"timeout", "Timeout", `{"tool":"DangEval","duration":"5m","arguments":{"source":"2 + 2"}}`, "1 │ 2 + 2"},
		{"write", "write", `{"filePath":"a.txt","contents":"hello\n"}`, "1 │ hello"},
		{"read", "read", `{"path":"main.go"}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, verbosity := range []int{dagui.ShowCompletedVerbosity, toolArgsVerbosity} {
				l := newLiveToolCalls(t, termenv.Ascii)
				l.fe.FrontendOpts.Verbosity = verbosity
				l.toolCall(liveTimeout, liveReply, c.tool)
				l.stream(liveTimeout, c.args, 1<<10, nil)
				l.closeArgs(liveTimeout)
				l.fe.viewDirty = true
				got := stripANSICodes(l.frame())
				args := strings.Index(got, c.args[:12])
				if verbosity < toolArgsVerbosity {
					assert.Equal(t, -1, args, "verbosity %d showed JSON arguments:\n%s", verbosity, got)
					continue
				}
				require.GreaterOrEqual(t, args, 0, "verbosity %d hid JSON arguments:\n%s", verbosity, got)
				if c.body != "" {
					assert.Less(t, strings.Index(got, c.body), args, "the body first:\n%s", got)
					assert.GreaterOrEqual(t, strings.Index(got, c.body), 0, got)
				}
			}
		})
	}
}

// expand expands a row as its toggle key does.
func (l *liveToolCalls) expand(id byte) {
	l.fe.setExpanded(prettyTestSpanID(id), true)
	l.fe.syncAfterExpandToggle(prettyTestSpanID(id))
}

// TestRichToolCallsHideTheirResult covers a tool call whose row renders its
// result richer than the raw result the model received: a Write's code body
// is the file it wrote, and a Check's CHECKS rollup what it ran. Collapsed,
// the raw result stays out; expanded, it shows. A DangEval's result is the
// value its snippet evaluates to, so it always shows, and so does the result
// of a call that errored.
func TestRichToolCallsHideTheirResult(t *testing.T) {
	result := func(l *liveToolCalls, id byte, text string) {
		l.logs(frontendTestLogRecord(trace.SpanID{id}, otellog.StringValue(text+"\n")))
	}
	collapsedAndExpanded := func(l *liveToolCalls, id byte) (string, string) {
		l.fe.viewDirty = true
		collapsed := stripANSICodes(l.frame())
		l.expand(id)
		expanded := stripANSICodes(l.frame())
		return collapsed, expanded
	}

	t.Run("write", func(t *testing.T) {
		l := newLiveToolCalls(t, termenv.Ascii)
		attrs := l.moduleToolCall(liveTimeout, liveReply, "editor", "write", "filePath", "contents")
		l.stream(liveTimeout, liveWriteArgs, 1<<10, nil)
		l.closeArgs(liveTimeout)
		result(l, liveTimeout, "RAW WRITE RESULT")
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(liveWriteArgs), &args))
		names, values := dagui.ToolHeaderArgs([]string{"filePath", "contents"}, args)
		l.endToolCall(liveTimeout, liveReply, "editor_write", attrs, names, values)
		collapsed, expanded := collapsedAndExpanded(l, liveTimeout)
		assert.Contains(t, collapsed, `5 │   return "hi"`, collapsed)
		assert.NotContains(t, collapsed, "RAW WRITE RESULT", collapsed)
		assert.Contains(t, expanded, "RAW WRITE RESULT", expanded)
	})

	t.Run("check", func(t *testing.T) {
		l := newLiveToolCalls(t, termenv.Ascii)
		attrs := l.moduleToolCall(liveTimeout, liveReply, "editor", "check", "include")
		l.stream(liveTimeout, `{"include":["**"]}`, 1<<10, nil)
		l.closeArgs(liveTimeout)
		checkAttr := attribute.String(telemetry.CheckNameAttr, "go:lint")
		l.span(liveWork, liveTimeout, "go:lint", checkAttr)
		l.export(liveWork, liveTimeout, "go:lint", true, checkAttr)
		result(l, liveTimeout, "== TESTS == RAW CHECK REPORT")
		l.endToolCall(liveTimeout, liveReply, "editor_check", attrs, []string{"include"}, []string{"**"})
		collapsed, expanded := collapsedAndExpanded(l, liveTimeout)
		assert.Contains(t, collapsed, "CHECKS", collapsed)
		assert.NotContains(t, collapsed, "RAW CHECK REPORT", collapsed)
		assert.Contains(t, expanded, "RAW CHECK REPORT", expanded)

		// The final report leaves it out too, unless it shows tool calls'
		// arguments: it can't be expanded.
		report := func() string {
			r := newRenderer(l.fe.db, 0, l.fe.FrontendOpts, true)
			return stripANSICodes(strings.Join(l.fe.conversationReport(tuist.Context{Width: 100}, r, false), "\n"))
		}
		got := report()
		assert.Contains(t, got, "CHECKS", got)
		assert.NotContains(t, got, "RAW CHECK REPORT", got)
		l.fe.FrontendOpts.Verbosity = toolArgsVerbosity
		got = report()
		assert.Contains(t, got, "RAW CHECK REPORT", got)
	})

	t.Run("services", func(t *testing.T) {
		// A SERVICES rollup says what's running, not what the call
		// returned of it: its endpoint, say.
		l := newLiveToolCalls(t, termenv.Ascii)
		attrs := l.moduleToolCall(liveTimeout, liveReply, "engineLab", "start")
		l.closeArgs(liveTimeout)
		l.span(liveWork, liveTimeout, "exec dagger engine",
			attribute.Bool(telemetryattrs.ServiceAttr, true),
			attribute.String(telemetryattrs.ServiceNameAttr, "engine.dagger.local"))
		result(l, liveTimeout, "ENDPOINT tcp://engine.dagger.local:1234")
		l.endToolCall(liveTimeout, liveReply, "engineLab_start", attrs, nil, nil)
		l.fe.viewDirty = true
		collapsed := stripANSICodes(l.frame())
		assert.Contains(t, collapsed, "SERVICES", collapsed)
		assert.Contains(t, collapsed, "ENDPOINT tcp://engine.dagger.local:1234", collapsed)

		r := newRenderer(l.fe.db, 0, l.fe.FrontendOpts, true)
		report := stripANSICodes(strings.Join(l.fe.conversationReport(tuist.Context{Width: 100}, r, false), "\n"))
		assert.Contains(t, report, "ENDPOINT tcp://engine.dagger.local:1234", report)
	})

	t.Run("a check that failed", func(t *testing.T) {
		l := newLiveToolCalls(t, termenv.Ascii)
		attrs := l.moduleToolCall(liveTimeout, liveReply, "editor", "check", "include")
		l.stream(liveTimeout, `{"include":["**"]}`, 1<<10, nil)
		l.closeArgs(liveTimeout)
		checkAttr := attribute.String(telemetry.CheckNameAttr, "go:lint")
		l.span(liveWork, liveTimeout, "go:lint", checkAttr)
		l.export(liveWork, liveTimeout, "go:lint", true, checkAttr)
		result(l, liveTimeout, "RAW CHECK ERROR")
		l.failToolCall(liveTimeout, liveReply, "editor_check", append(attrs,
			attribute.StringSlice(telemetry.LLMToolArgNamesAttr, []string{"include"}),
			attribute.StringSlice(telemetry.LLMToolArgValuesAttr, []string{"**"})), "check failed")
		l.fe.viewDirty = true
		collapsed := stripANSICodes(l.frame())
		assert.Contains(t, collapsed, "RAW CHECK ERROR", collapsed)
	})

	t.Run("dang eval", func(t *testing.T) {
		l := newLiveToolCalls(t, termenv.Ascii)
		l.toolCall(liveTimeout, liveReply, "DangEval")
		l.stream(liveTimeout, `{"source":"ws.check"}`, 1<<10, nil)
		l.closeArgs(liveTimeout)
		checkAttr := attribute.String(telemetry.CheckNameAttr, "go:lint")
		l.span(liveWork, liveTimeout, "go:lint", checkAttr)
		l.export(liveWork, liveTimeout, "go:lint", true, checkAttr)
		result(l, liveTimeout, `CheckGroup! = "7 passed"`)
		l.fe.viewDirty = true
		collapsed := stripANSICodes(l.frame())
		assert.Contains(t, collapsed, `1 │ ws.check`, collapsed)
		assert.Contains(t, collapsed, "CHECKS", collapsed)
		assert.Contains(t, collapsed, `CheckGroup! = "7 passed"`, "a DangEval's value, even beside a rollup:\n%s", collapsed)
	})

	t.Run("timeout of dang eval", func(t *testing.T) {
		l := newLiveToolCalls(t, termenv.Ascii)
		l.toolCall(liveTimeout, liveReply, "Timeout")
		l.stream(liveTimeout, liveTimeoutArgs, 1<<10, nil)
		l.closeArgs(liveTimeout)
		result(l, liveTimeout, `String! = "HI!"`)
		l.fe.viewDirty = true
		collapsed := stripANSICodes(l.frame())
		assert.Contains(t, collapsed, `1 │ let greeting`, collapsed)
		assert.Contains(t, collapsed, `String! = "HI!"`, collapsed)
	})
}
