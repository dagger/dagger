package dagui

import (
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

// toolHeaderDB is a DB fed tool calls as a live session feeds it: each call's
// display span starts naming its header's arguments, its JSON arguments
// stream in, and it ends with the header values the engine recorded.
type toolHeaderDB struct {
	t     *testing.T
	db    *DB
	start time.Time
}

var toolHeaderTraceID = trace.TraceID{1}

func newToolHeaderDB(t *testing.T) *toolHeaderDB {
	return &toolHeaderDB{t: t, db: NewDB(), start: time.Unix(100, 0)}
}

func (h *toolHeaderDB) call(id byte, server, tool string, headerArgs ...string) *Span {
	spanID := SpanID{SpanID: trace.SpanID{id}}
	h.db.ImportSnapshots([]SpanSnapshot{{
		ID: spanID, TraceID: TraceID{toolHeaderTraceID}, Name: tool,
		LLMRole: "assistant", LLMTool: tool, LLMToolServer: server,
		LLMToolHeaderArgs: headerArgs,
		StartTime:         h.start,
	}})
	return h.db.Spans.Map[spanID]
}

func (h *toolHeaderDB) stream(span *Span, chunks ...string) {
	for _, chunk := range chunks {
		h.db.IngestLogs([]sdklog.Record{newTestLogRecord(toolHeaderTraceID, span.ID.SpanID, chunk,
			otellog.String(telemetry.ContentTypeAttr, "application/json"))})
	}
}

// end ends the call as MCP.CallContent does, with the header values it
// records from args.
func (h *toolHeaderDB) end(span *Span, args map[string]any) {
	snapshot := span.Snapshot()
	snapshot.EndTime = h.start.Add(time.Second)
	snapshot.LLMToolArgNames, snapshot.LLMToolArgValues = ToolHeaderArgs(snapshot.LLMToolHeaderArgs, args)
	h.db.ImportSnapshots([]SpanSnapshot{snapshot})
}

// TestToolArgsSummaryStreams covers a module tool call's header while it
// runs: the arguments its display span named are filled in from its
// streamed arguments as they arrive, a list with its whole elements so far,
// and it's the header the call ends with, once its arguments are freed.
func TestToolArgsSummaryStreams(t *testing.T) {
	h := newToolHeaderDB(t)
	goCall := h.call(2, "goCli", "go", "args", "offset", "limit")
	summary := func() []ToolArgPart { return h.db.ToolArgsSummary(goCall) }

	assert.Empty(t, summary(), "nothing streamed yet")
	h.stream(goCall, `{"args": ["vet", "./engine/cli`)
	assert.Equal(t, []ToolArgPart{{Text: "vet", Style: ToolArgContent}}, summary(),
		"a list shows its whole elements, never a partial one")
	h.stream(goCall, `entdb/", "./engine/server/", `)
	assert.Equal(t, []ToolArgPart{{Text: "vet ./engine/clientdb/ ./engine/server/", Style: ToolArgContent}}, summary())
	h.stream(goCall, `"-count", 3`)
	assert.Equal(t, []ToolArgPart{{Text: "vet ./engine/clientdb/ ./engine/server/ -count", Style: ToolArgContent}}, summary(),
		"a number that may still grow is left out")
	h.stream(goCall, `], "env": ["X=1"]}`+"\n")
	want := []ToolArgPart{{Text: "vet ./engine/clientdb/ ./engine/server/ -count 3", Style: ToolArgContent}}
	assert.Equal(t, want, summary())
	assert.Equal(t, "vet ./engine/clientdb/ ./engine/server/ -count 3", h.db.ToolArgsFallback(goCall))

	// Once it ends, its attributes speak, and say the same.
	h.end(goCall, map[string]any{
		"args": []any{"vet", "./engine/clientdb/", "./engine/server/", "-count", float64(3)},
		"env":  []any{"X=1"},
	})
	assert.Equal(t, want, summary())
	assert.NotContains(t, h.db.toolArgsJSON, goCall.ID, "an ended call's streamed arguments are freed")
	// Late records don't bring them back.
	h.stream(goCall, `{"args":["late"]}`)
	assert.NotContains(t, h.db.toolArgsJSON, goCall.ID)
	assert.Equal(t, want, summary())
}

// TestToolArgsSummaryStreamsWholeValues covers strings cut short: a header
// shows an argument once its value is whole, as a partial path or name
// would only mislead.
func TestToolArgsSummaryStreamsWholeValues(t *testing.T) {
	h := newToolHeaderDB(t)
	read := h.call(2, "editor", "read", "filePath", "offset", "limit", "args")
	h.stream(read, `{"filePath":"dagql/dagui/tool`)
	assert.Empty(t, h.db.ToolArgsSummary(read))
	assert.Empty(t, h.db.ToolArgsFallback(read))
	h.stream(read, `call.go","offset":2`)
	assert.Equal(t, []ToolArgPart{{Text: "dagql/dagui/toolcall.go", Style: ToolArgPath}}, h.db.ToolArgsSummary(read))
	h.stream(read, `0,"limit":10}`)
	assert.Equal(t, []ToolArgPart{
		{Text: "dagql/dagui/toolcall.go", Style: ToolArgPath},
		{Text: "offset=20", Style: ToolArgDescription},
		{Text: "limit=10", Style: ToolArgDescription},
	}, h.db.ToolArgsSummary(read))

	// An unrecognized tool falls back to its first header argument.
	custom := h.call(3, "notes", "remember", "topic", "body")
	h.stream(custom, `{"topic":"bugs","body":"a long`)
	assert.Empty(t, h.db.ToolArgsSummary(custom))
	assert.Equal(t, "bugs", h.db.ToolArgsFallback(custom))

	// Write's contents, which it shows as they stream, aren't a value its
	// header shows until whole.
	write := h.call(4, "editor", "write", "filePath", "contents")
	h.stream(write, `{"filePath":"a.txt","contents":"line 1\nli`)
	assert.Equal(t, []ToolArgPart{{Text: "a.txt", Style: ToolArgPath}}, h.db.ToolArgsSummary(write))
	view := h.db.ToolCallView(write)
	assert.True(t, view.Streaming)
	file, ok := view.WriteFile()
	require.True(t, ok)
	assert.Equal(t, WriteFile{Path: "a.txt", Contents: "line 1\nli"}, file)
}

// TestToolArgsSummaryWithoutHeaderArgs covers spans that don't name their
// header's arguments -- an engine that predates them, or a tool the turn
// didn't offer: their header is their attributes', as it was.
func TestToolArgsSummaryWithoutHeaderArgs(t *testing.T) {
	h := newToolHeaderDB(t)
	read := h.call(2, "", "read")
	h.stream(read, `{"path":"main.go"}`)
	assert.Empty(t, h.db.ToolArgsSummary(read))
	assert.Empty(t, h.db.ToolArgsFallback(read))

	var nilDB *DB
	assert.Empty(t, nilDB.ToolArgsSummary(read))
	assert.Empty(t, nilDB.ToolArgsSummary(nil))
	assert.Empty(t, nilDB.ToolArgsFallback(nil))
}

// TestToolArgsFreedWhenCallEnds covers the memory a running call's
// arguments hold: freed once it ends, unless they were cut short by an
// aborted request -- the only record of them there is.
func TestToolArgsFreedWhenCallEnds(t *testing.T) {
	h := newToolHeaderDB(t)
	endWith := func(span *Span, status codes.Code) {
		snapshot := span.Snapshot()
		snapshot.EndTime = h.start.Add(time.Second)
		snapshot.Status = sdktrace.Status{Code: status}
		h.db.ImportSnapshots([]SpanSnapshot{snapshot})
	}

	// A tool with no header arguments ends with none recorded.
	status := h.call(2, "committer", "status")
	h.stream(status, `{"paths":["src/**"]}`)
	require.Contains(t, h.db.toolArgsJSON, status.ID)
	endWith(status, codes.Ok)
	assert.NotContains(t, h.db.toolArgsJSON, status.ID)

	aborted := h.call(3, "editor", "write", "filePath", "contents")
	h.stream(aborted, `{"filePath":"a.txt","contents":"hel`)
	endWith(aborted, codes.Error)
	assert.Contains(t, h.db.toolArgsJSON, aborted.ID)
	assert.Equal(t, []ToolArgPart{{Text: "a.txt", Style: ToolArgPath}}, h.db.ToolArgsSummary(aborted))
	view := h.db.ToolCallView(aborted)
	assert.False(t, view.Streaming)
	file, ok := view.WriteFile()
	require.True(t, ok)
	assert.Equal(t, "hel", file.Contents)

	// Records that arrive after their call ended -- replayed, read on
	// demand, or the last batch landing late -- aren't kept once whole.
	late := h.call(4, "committer", "log")
	endWith(late, codes.Ok)
	h.stream(late, `{"limit":`)
	assert.Contains(t, h.db.toolArgsJSON, late.ID, "cut short: kept")
	h.stream(late, `5}`+"\n")
	assert.NotContains(t, h.db.toolArgsJSON, late.ID)
}

func TestWriteFile(t *testing.T) {
	file, ok := NewToolCallView("write", map[string]any{"filePath": "a.go", "contents": "package a\n"}).WriteFile()
	require.True(t, ok)
	assert.Equal(t, WriteFile{Path: "a.go", Contents: "package a\n"}, file)

	// Its contents may be on their way.
	file, ok = NewToolCallView("editor_write", map[string]any{"filePath": "a.go"}).WriteFile()
	require.True(t, ok)
	assert.Equal(t, WriteFile{Path: "a.go"}, file)

	// A Timeout presents as the Write it wraps.
	file, ok = NewToolCallView("Timeout", map[string]any{
		"tool": "editor_write", "duration": "1m",
		"arguments": map[string]any{"filePath": "a.go", "contents": "x"},
	}).WriteFile()
	require.True(t, ok)
	assert.Equal(t, "x", file.Contents)

	// Not a Write of a file: another write, another tool, or no file.
	_, ok = NewToolCallView("write", map[string]any{"path": "a.go", "contents": "x"}).WriteFile()
	assert.False(t, ok)
	_, ok = NewToolCallView("read", map[string]any{"filePath": "a.go"}).WriteFile()
	assert.False(t, ok)
	_, ok = NewToolCallView("write", map[string]any{"filePath": "a.go", "contents": 3.0}).WriteFile()
	assert.False(t, ok)
	_, ok = NewToolCallView("write", map[string]any{"contents": "x"}).WriteFile()
	assert.False(t, ok, "a finished Write names its file")
	assert.True(t, RendersToolArgs("editor_write"))

	// While its arguments stream in, before its path is whole, a call is a
	// Write if what it has so far is Write's.
	streaming := NewToolCallView("editor_write", map[string]any{})
	streaming.Streaming = true
	file, ok = streaming.WriteFile()
	require.True(t, ok)
	assert.Equal(t, WriteFile{}, file)
	streaming.Args = map[string]any{"force": true, "contents": "x"}
	_, ok = streaming.WriteFile()
	assert.True(t, ok)
	streaming.Args = map[string]any{"path": "a.go"}
	_, ok = streaming.WriteFile()
	assert.False(t, ok)
}

// TestWriteContentsStreamInsideTimeout covers a Timeout wrapping Write: the
// wrapped call's contents show as they stream, as its own would.
func TestWriteContentsStreamInsideTimeout(t *testing.T) {
	h := newToolHeaderDB(t)
	timeout := h.call(2, "", "Timeout", "tool", "duration", "arguments")
	h.stream(timeout, `{"tool":"editor_write","duration":"1m","arguments":{"filePath":"a.txt","contents":"one\ntw`)
	view := h.db.ToolCallView(timeout)
	assert.True(t, view.Wrapped())
	file, ok := view.WriteFile()
	require.True(t, ok)
	assert.Equal(t, WriteFile{Path: "a.txt", Contents: "one\ntw"}, file)

	// Not another tool's argument of that name.
	other := h.call(3, "", "Timeout", "tool", "duration", "arguments")
	h.stream(other, `{"tool":"notes_save","duration":"1m","arguments":{"filePath":"a.txt","contents":"one\ntw`)
	assert.Equal(t, map[string]any{"filePath": "a.txt"}, h.db.ToolCallView(other).Args)
}
