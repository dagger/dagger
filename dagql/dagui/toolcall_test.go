package dagui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	telemetry "github.com/dagger/otel-go"
)

func TestNewToolCallView(t *testing.T) {
	// An ordinary call presents as itself.
	view := NewToolCallView("Read", map[string]any{"path": "main.go"})
	assert.Equal(t, "Read", view.Tool)
	assert.False(t, view.Wrapped())

	// A Timeout presents as the call it wraps, from JSON arguments...
	view = NewToolCallView("Timeout", map[string]any{
		"duration":  "2m",
		"tool":      "DangEval",
		"arguments": map[string]any{"source": "1 + 1", "at": "notes"},
	})
	assert.Equal(t, ToolCallView{
		Tool:    "DangEval",
		Args:    map[string]any{"source": "1 + 1", "at": "notes"},
		Timeout: "2m",
	}, view)
	assert.True(t, view.Wrapped())

	// ...or from span attributes, which carry them as a JSON string.
	view = NewToolCallView("Timeout", map[string]any{
		"duration":  "30s",
		"tool":      "read",
		"arguments": `{"path":"main.go","offset":20}`,
	})
	assert.Equal(t, "read", view.Tool)
	assert.Equal(t, "30s", view.Timeout)
	assert.Equal(t, []ToolArgPart{
		{Text: "main.go", Style: ToolArgPath},
		{Text: "offset=20", Style: ToolArgDescription},
	}, view.ArgsSummary())

	// A Timeout whose wrapped tool isn't known yet presents as itself.
	view = NewToolCallView("Timeout", map[string]any{"duration": "2m"})
	assert.Equal(t, "Timeout", view.Tool)
	assert.False(t, view.Wrapped())

	// A wrapped tool with no duration is still wrapped.
	view = NewToolCallView("Timeout", map[string]any{"tool": "frobnicate"})
	assert.Equal(t, "?", view.Timeout)
	assert.Nil(t, view.Args)
}

func TestToolCallViewArgsFallback(t *testing.T) {
	view := NewToolCallView("frobnicate", map[string]any{
		"name":  "first\nsecond",
		"count": 3.0,
		"opts":  map[string]any{"x": true},
	})
	// Unrecognized: no summary; the fallback is the first argument by name.
	assert.Empty(t, view.ArgsSummary())
	assert.Equal(t, "3", view.ArgsFallback())
	assert.Equal(t, "", NewToolCallView("frobnicate", nil).ArgsFallback())
}

func TestToolCallViewWrapsAndCollapses(t *testing.T) {
	traceID := trace.TraceID{1}
	timeoutID := SpanID{SpanID: trace.SpanID{2}}
	nestedID := SpanID{SpanID: trace.SpanID{3}}
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID: timeoutID, TraceID: TraceID{traceID}, Name: "Timeout",
			LLMRole: "assistant", LLMTool: "Timeout",
			LLMToolArgNames:  []string{"arguments", "duration", "tool"},
			LLMToolArgValues: []string{`{"path":"main.go"}`, "30s", "editor_read"},
		},
		{
			ID: nestedID, TraceID: TraceID{traceID}, Name: "read", ParentID: timeoutID,
			LLMRole: "assistant", LLMTool: "read", LLMToolServer: "Editor",
		},
	})
	timeout, nested := db.Spans.Map[timeoutID], db.Spans.Map[nestedID]

	// The span names the tool bare; the model, with its server.
	assert.True(t, db.ToolCallView(timeout).Wraps(nested))
	assert.Equal(t, nested, db.WrappedCallSpan(timeout))
	assert.Nil(t, db.WrappedCallSpan(nested))
	assert.True(t, NewToolCallView("Timeout", map[string]any{"tool": "read_file"}).
		Wraps(&Span{SpanSnapshot: SpanSnapshot{LLMRole: "assistant", LLMTool: "read_file"}}))
	assert.False(t, NewToolCallView("Timeout", map[string]any{"tool": "write"}).Wraps(nested))
	// A snake_case tool of a server: its span is named as the model calls it.
	snake := &Span{SpanSnapshot: SpanSnapshot{
		Name: "github_create_issue", LLMRole: "assistant", LLMTool: "create_issue", LLMToolServer: "github",
	}}
	assert.True(t, NewToolCallView("Timeout", map[string]any{"tool": "github_create_issue"}).Wraps(snake))
	snake.Name = ""
	assert.True(t, NewToolCallView("Timeout", map[string]any{"tool": "github_create_issue"}).Wraps(snake))

	// A Timeout running Read collapses as Read does.
	assert.False(t, timeout.CollapsesToolOutput())
	assert.True(t, db.CollapsesToolOutput(timeout))
	assert.True(t, db.CollapsesToolOutput(nested))
	assert.False(t, db.CollapsesToolOutput(nil))
}

func TestDangSnippet(t *testing.T) {
	snippet, ok := NewToolCallView("DangEval", map[string]any{"source": "1", "at": "notes"}).DangSnippet()
	require.True(t, ok)
	assert.Equal(t, DangSnippet{Source: "1", At: "notes"}, snippet)

	snippet, ok = NewToolCallView("DangCheck", map[string]any{"search": "sort"}).DangSnippet()
	require.True(t, ok)
	assert.Equal(t, DangSnippet{Search: "sort"}, snippet)

	_, ok = NewToolCallView("Read", map[string]any{"source": "x"}).DangSnippet()
	assert.False(t, ok)

	assert.True(t, RendersToolArgs("DangEval"))
	assert.True(t, RendersToolArgs("dangcheck"))
	assert.True(t, RendersToolArgs("Timeout"))
	assert.False(t, RendersToolArgs("Read"))
}

// TestToolCallArgsFromStreamedJSON covers a live tool call: its arguments
// stream as JSON logs on its display span before its attributes arrive
// (which only happens once it ends), and are what it renders from once the
// document is complete.
func TestToolCallArgsFromStreamedJSON(t *testing.T) {
	traceID := trace.TraceID{1}
	toolID := SpanID{SpanID: trace.SpanID{2}}
	plainID := SpanID{SpanID: trace.SpanID{3}}
	start := time.Unix(100, 0)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID: toolID, TraceID: TraceID{traceID}, Name: "Timeout",
			LLMRole: "assistant", LLMTool: "Timeout", StartTime: start,
		},
		{
			ID: plainID, TraceID: TraceID{traceID}, Name: "Read",
			LLMRole: "assistant", LLMTool: "Read", StartTime: start,
		},
	})
	jsonLog := func(id SpanID, body string) {
		db.IngestLogs([]sdklog.Record{newTestLogRecord(traceID, id.SpanID, body,
			otellog.String(telemetry.ContentTypeAttr, "application/json"))})
	}
	tool := db.Spans.Map[toolID]

	// Nothing yet.
	assert.Nil(t, db.ToolCallArgs(tool))

	// Incomplete: what arrived so far. A Timeout presents as the call it
	// wraps as soon as it's named, with its source as far as it got.
	jsonLog(toolID, `{"duration":"2m","tool":"DangEval",`)
	assert.Equal(t, map[string]any{"duration": "2m", "tool": "DangEval"}, db.ToolCallArgs(tool))
	view := db.ToolCallView(tool)
	assert.True(t, view.Wrapped())
	assert.True(t, view.Streaming)
	assert.Equal(t, "DangEval", view.Tool)

	jsonLog(toolID, `"arguments":{"source":"1 + `)
	view = db.ToolCallView(tool)
	assert.True(t, view.Streaming)
	assert.Equal(t, map[string]any{"source": "1 + "}, view.Args)

	jsonLog(toolID, `1"}}`+"\n")
	view = db.ToolCallView(tool)
	assert.False(t, view.Streaming)
	assert.Equal(t, "DangEval", view.Tool)
	assert.Equal(t, "2m", view.Timeout)
	assert.Equal(t, map[string]any{"source": "1 + 1"}, view.Args)

	// Tools that don't render from their arguments keep nothing.
	jsonLog(plainID, `{"path":"main.go"}`)
	assert.Nil(t, db.ToolCallArgs(db.Spans.Map[plainID]))

	// Arguments that arrive before their span are kept until the span
	// turns out not to render from them...
	earlyID := SpanID{SpanID: trace.SpanID{4}}
	jsonLog(earlyID, `{"path":"a.txt","contents":"big"}`)
	assert.Contains(t, db.toolArgsJSON, earlyID)
	db.ImportSnapshots([]SpanSnapshot{{
		ID: earlyID, TraceID: TraceID{traceID}, Name: "write",
		LLMRole: "assistant", LLMTool: "write", StartTime: start,
	}})
	assert.NotContains(t, db.toolArgsJSON, earlyID)
	// ...or does.
	earlyEvalID := SpanID{SpanID: trace.SpanID{5}}
	jsonLog(earlyEvalID, `{"source":"1"}`)
	db.ImportSnapshots([]SpanSnapshot{{
		ID: earlyEvalID, TraceID: TraceID{traceID}, Name: "DangEval",
		LLMRole: "assistant", LLMTool: "DangEval", StartTime: start,
	}})
	assert.Equal(t, map[string]any{"source": "1"}, db.ToolCallArgs(db.Spans.Map[earlyEvalID]))
}

func TestToolCallArgsFallsBackToAttributes(t *testing.T) {
	traceID := trace.TraceID{1}
	toolID := SpanID{SpanID: trace.SpanID{2}}
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{{
		ID: toolID, TraceID: TraceID{traceID}, Name: "DangEval",
		LLMRole: "assistant", LLMTool: "DangEval",
		LLMToolArgNames:  []string{"at", "source"},
		LLMToolArgValues: []string{"notes", "shout(\"x\")"},
	}})
	tool := db.Spans.Map[toolID]
	assert.Equal(t, map[string]any{"at": "notes", "source": "shout(\"x\")"}, db.ToolCallArgs(tool))

	// Arguments too large to keep fall back to the attributes too.
	db.IngestLogs([]sdklog.Record{newTestLogRecord(traceID, toolID.SpanID,
		`{"source":"`+strings.Repeat("x", maxToolArgsJSON)+`"}`,
		otellog.String(telemetry.ContentTypeAttr, "application/json"))})
	assert.Equal(t, "notes", db.ToolCallArgs(tool)["at"])

	// No DB: attributes alone.
	var nilDB *DB
	assert.Equal(t, "notes", nilDB.ToolCallArgs(tool)["at"])
	assert.Nil(t, nilDB.ToolCallArgs(nil))
}

// TestToolCallArgsWhileStreaming covers what a call shows of arguments still
// streaming in: nothing that could mislead, and attributes over a stream
// that never completed.
func TestToolCallArgsWhileStreaming(t *testing.T) {
	traceID := trace.TraceID{1}
	start := time.Unix(100, 0)
	db := NewDB()
	newCall := func(id byte, tool string) *Span {
		spanID := SpanID{SpanID: trace.SpanID{id}}
		db.ImportSnapshots([]SpanSnapshot{{
			ID: spanID, TraceID: TraceID{traceID}, Name: tool,
			LLMRole: "assistant", LLMTool: tool, StartTime: start,
		}})
		return db.Spans.Map[spanID]
	}
	jsonLog := func(span *Span, body string) {
		db.IngestLogs([]sdklog.Record{newTestLogRecord(traceID, span.ID.SpanID, body,
			otellog.String(telemetry.ContentTypeAttr, "application/json"))})
	}

	// A tool name cut short is no name at all: the Timeout presents as
	// itself until it's whole.
	timeout := newCall(2, "Timeout")
	jsonLog(timeout, `{"duration":"5m","tool":"Dang`)
	view := db.ToolCallView(timeout)
	assert.False(t, view.Wrapped())
	assert.Equal(t, "Timeout", view.Tool)
	assert.True(t, view.Streaming)
	jsonLog(timeout, `Eval","arguments":{"at":"no`)
	view = db.ToolCallView(timeout)
	assert.Equal(t, "DangEval", view.Tool)
	assert.Equal(t, map[string]any{}, view.Args, "a partial scope is left out")

	// So is a partial element of a list.
	eval := newCall(3, "DangEval")
	jsonLog(eval, `{"source":"1","tags":["a","b`)
	assert.Equal(t, map[string]any{"source": "1", "tags": []any{"a"}}, db.ToolCallArgs(eval))

	// Only a Dang snippet's source shows as it grows, not a wrapped tool's
	// argument of the same name.
	wrapsBuild := newCall(6, "Timeout")
	jsonLog(wrapsBuild, `{"duration":"5m","tool":"go_build","arguments":{"source":"dag://wor`)
	view = db.ToolCallView(wrapsBuild)
	assert.Equal(t, "go_build", view.Tool)
	assert.Equal(t, map[string]any{}, view.Args)
	assert.Equal(t, "", view.ArgsFallback())

	// A call that ended before its arguments did -- its request aborted --
	// keeps what it got, but it isn't streaming anymore.
	aborted := newCall(8, "Timeout")
	jsonLog(aborted, `{"duration":"5m","tool":"DangEval","arguments":{"source":"let x = `)
	assert.True(t, db.ToolCallView(aborted).Streaming)
	snapshot := aborted.Snapshot()
	snapshot.EndTime = start.Add(time.Second)
	snapshot.Status = sdktrace.Status{Code: codes.Error, Description: "request failed"}
	db.ImportSnapshots([]SpanSnapshot{snapshot})
	view = db.ToolCallView(aborted)
	assert.False(t, view.Streaming)
	assert.Equal(t, map[string]any{"source": "let x = "}, view.Args)

	// Once a call ends, its attributes have its whole arguments, whatever
	// streamed.
	broken := newCall(4, "DangEval")
	jsonLog(broken, `{"source":"let x`)
	assert.Equal(t, map[string]any{"source": "let x"}, db.ToolCallArgs(broken))
	snapshot = broken.Snapshot()
	snapshot.EndTime = start.Add(time.Second)
	snapshot.LLMToolArgNames, snapshot.LLMToolArgValues = []string{"source"}, []string{"let x = 1"}
	db.ImportSnapshots([]SpanSnapshot{snapshot})
	view = db.ToolCallView(broken)
	assert.False(t, view.Streaming)
	assert.Equal(t, map[string]any{"source": "let x = 1"}, view.Args)

	// What follows the arguments in their stream is not part of them.
	trailing := newCall(5, "DangEval")
	jsonLog(trailing, `{"source":"1"}`+"\n")
	jsonLog(trailing, `{"oops":true}`+"\n")
	view = db.ToolCallView(trailing)
	assert.False(t, view.Streaming)
	assert.Equal(t, map[string]any{"source": "1"}, view.Args)
}
