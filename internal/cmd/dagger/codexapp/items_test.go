package codexapp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordingSink captures notifications in order.
type recordingSink struct {
	mu    sync.Mutex
	notes []notification
}

type notification struct {
	Method string
	Params any
}

func (r *recordingSink) Notify(method string, params any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, notification{Method: method, Params: params})
	return nil
}

func (r *recordingSink) all() []notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notification{}, r.notes...)
}

func (r *recordingSink) methods() []string {
	var methods []string
	for _, n := range r.all() {
		methods = append(methods, n.Method)
	}
	return methods
}

// find returns the params of the nth notification with the given method.
func (r *recordingSink) find(method string, n int) any {
	for _, note := range r.all() {
		if note.Method != method {
			continue
		}
		if n == 0 {
			return note.Params
		}
		n--
	}
	return nil
}

var (
	testTraceID   = trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	nextSpanIDSeq atomic.Uint64
)

// testSpan builds running and ended snapshots of one span.
type testSpan struct {
	id     trace.SpanID
	parent trace.SpanID
	name   string
	attrs  []attribute.KeyValue
	start  time.Time
}

func newTestSpan(name string, attrs ...attribute.KeyValue) *testSpan {
	seq := nextSpanIDSeq.Add(1)
	var id trace.SpanID
	id[7] = byte(seq)
	id[6] = byte(seq >> 8)
	return &testSpan{id: id, name: name, attrs: attrs, start: time.Now()}
}

func (s *testSpan) stub(end time.Time, status sdktrace.Status) sdktrace.ReadOnlySpan {
	stub := tracetest.SpanStub{
		Name:        s.name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: testTraceID, SpanID: s.id}),
		StartTime:   s.start,
		EndTime:     end,
		Attributes:  s.attrs,
		Status:      status,
	}
	if s.parent.IsValid() {
		stub.Parent = trace.NewSpanContext(trace.SpanContextConfig{TraceID: testTraceID, SpanID: s.parent})
	}
	return stub.Snapshot()
}

func (s *testSpan) running() sdktrace.ReadOnlySpan {
	return s.stub(time.Time{}, sdktrace.Status{})
}

func (s *testSpan) ended() sdktrace.ReadOnlySpan {
	return s.stub(time.Now(), sdktrace.Status{Code: codes.Unset})
}

func (s *testSpan) failed(desc string) sdktrace.ReadOnlySpan {
	return s.stub(time.Now(), sdktrace.Status{Code: codes.Error, Description: desc})
}

func agentAttrs(agentID string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(genAIAgentIDAttr, agentID),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
	}
}

func record(spanID trace.SpanID, body string, attrs ...log.KeyValue) sdklog.Record {
	var rec sdklog.Record
	rec.SetTraceID(testTraceID)
	rec.SetSpanID(spanID)
	rec.SetBody(log.StringValue(body))
	rec.AddAttributes(attrs...)
	return rec
}

// markdown is a text-phase record: streamed by a plain writer with a content
// type and no stdio stream, as core/llm_display.go's MarkdownW emits.
func markdown(spanID trace.SpanID, body string) sdklog.Record {
	return record(spanID, body, log.String(telemetry.ContentTypeAttr, "text/markdown"))
}

func stdout(spanID trace.SpanID, body string) sdklog.Record {
	return record(spanID, body, log.Int(telemetry.StdioStreamAttr, 1))
}

func stderr(spanID trace.SpanID, body string) sdklog.Record {
	return record(spanID, body, log.Int(telemetry.StdioStreamAttr, 2))
}

func eof(spanID trace.SpanID, stream int) sdklog.Record {
	return record(spanID, "", log.Int(telemetry.StdioStreamAttr, stream), log.Bool(telemetry.StdioEOFAttr, true))
}

func newTestStream(t *testing.T, sink Sink, ref TurnRef, agentID string) *ItemStream {
	t.Helper()
	s := NewItemStream(sink)
	s.grace = 100 * time.Millisecond
	var seq int
	s.newID = func() string {
		seq++
		return "item-" + string(rune('0'+seq))
	}
	s.SetResolver(func(id string) (TurnRef, bool) {
		if id == agentID {
			return ref, true
		}
		return TurnRef{}, false
	})
	s.BeginTurn(ref)
	return s
}

func TestItemStreamAgentMessage(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	span := newTestSpan("LLM response", agentAttrs("agent-1")...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{markdown(span.id, "Hello"), markdown(span.id, ", world")}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))

	// The span ended but its stream has not closed: the item stays open for
	// trailing deltas.
	require.NotContains(t, sink.methods(), NotifyItemCompleted)
	require.NoError(t, s.Export(ctx, []sdklog.Record{markdown(span.id, "!"), eof(span.id, 1), eof(span.id, 2)}))

	saw := s.EndTurn(ref, true, 0, time.Second)
	require.True(t, saw)
	require.Equal(t, []string{
		NotifyItemStarted,
		NotifyAgentMessageDelta,
		NotifyAgentMessageDelta,
		NotifyAgentMessageDelta,
		NotifyItemCompleted,
	}, sink.methods())

	started := sink.find(NotifyItemStarted, 0).(ItemStartedNotification)
	require.Equal(t, ref.ThreadID, started.ThreadID)
	require.Equal(t, ref.TurnID, started.TurnID)
	require.Equal(t, NewAgentMessageItem("item-1", ""), started.Item)

	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewAgentMessageItem("item-1", "Hello, world!"), completed.Item)
}

func TestItemStreamBuffersLogsThatPrecedeTheirSpan(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	span := newTestSpan("LLM response", agentAttrs("agent-1")...)
	require.NoError(t, s.Export(ctx, []sdklog.Record{markdown(span.id, "early")}))
	require.Empty(t, sink.methods())
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{eof(span.id, 1)}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))

	s.EndTurn(ref, true, 0, time.Second)
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewAgentMessageItem("item-1", "early"), completed.Item)
}

func TestItemStreamCompletesAfterGraceWithoutEOF(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	span := newTestSpan("LLM response", agentAttrs("agent-1")...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{markdown(span.id, "done")}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))
	require.NotContains(t, sink.methods(), NotifyItemCompleted)
	require.Eventually(t, func() bool {
		for _, m := range sink.methods() {
			if m == NotifyItemCompleted {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewAgentMessageItem("item-1", "done"), completed.Item)
}

func TestItemStreamToolCall(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	attrs := append(agentAttrs("agent-1"), attribute.String(telemetry.LLMToolAttr, "Workspace.withNewFile"))
	span := newTestSpan("Workspace.withNewFile", attrs...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	// Arguments stream first; the call is underway once they close.
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, `{"path":`), stdout(span.id, `"a.txt"}`)}))
	require.Empty(t, sink.methods())
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, "\n"), eof(span.id, 1)}))
	require.Equal(t, []string{NotifyItemStarted}, sink.methods())
	started := sink.find(NotifyItemStarted, 0).(ItemStartedNotification).Item.(McpToolCallItem)
	require.Equal(t, DaggerToolServer, started.Server)
	require.Equal(t, "Workspace.withNewFile", started.Tool)
	require.Equal(t, ToolCallStatusInProgress, started.Status)
	require.JSONEq(t, `{"path":"a.txt"}`, string(started.Arguments.(json.RawMessage)))

	// Then the result the model saw, closed by a second EOF.
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, "wrote a.txt\n"), eof(span.id, 1), eof(span.id, 2)}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))

	s.EndTurn(ref, false, 0, time.Second)
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification).Item.(McpToolCallItem)
	require.Equal(t, ToolCallStatusCompleted, completed.Status)
	require.Equal(t, TextResult("wrote a.txt"), completed.Result)
	require.Nil(t, completed.Error)
	require.NotNil(t, completed.DurationMs)
}

func TestItemStreamFailedToolCallAndMCPServer(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	attrs := append(agentAttrs("agent-1"),
		attribute.String(telemetry.LLMToolAttr, "search"),
		attribute.String(telemetry.LLMToolServerAttr, "docs"))
	span := newTestSpan("search", attrs...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, `{"q":"x"}`), eof(span.id, 1)}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, "no such index\n"), eof(span.id, 1)}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.failed(`tool call "search" failed`)}))

	s.EndTurn(ref, false, 0, time.Second)
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification).Item.(McpToolCallItem)
	require.Equal(t, "docs", completed.Server)
	require.Equal(t, ToolCallStatusFailed, completed.Status)
	require.Equal(t, TextResult("no such index"), completed.Result)
	require.Equal(t, &McpToolCallError{Message: `tool call "search" failed`}, completed.Error)
}

func TestItemStreamToolCallEndedBeforeArgumentsClose(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	attrs := append(agentAttrs("agent-1"), attribute.String(telemetry.LLMToolAttr, "shell"))
	span := newTestSpan("shell", attrs...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, `{"cmd":`)}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))

	// An interrupted call never closed its arguments: it is still reported,
	// started and completed back to back, with the raw partial arguments.
	s.EndTurn(ref, false, 0, time.Second)
	require.Equal(t, []string{NotifyItemStarted, NotifyItemCompleted}, sink.methods())
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification).Item.(McpToolCallItem)
	require.Equal(t, `{"cmd":`, completed.Arguments)
}

func TestItemStreamReasoning(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	attrs := append(agentAttrs("agent-1"), attribute.Bool(llmThinkingAttr, true))
	span := newTestSpan("thinking", attrs...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.id, "Let me "), stdout(span.id, "think."), eof(span.id, 1)}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))

	saw := s.EndTurn(ref, false, 0, time.Second)
	require.False(t, saw, "reasoning is not a reply")
	require.Equal(t, []string{
		NotifyItemStarted,
		NotifyReasoningSummaryPart,
		NotifyReasoningSummaryDelta,
		NotifyReasoningSummaryDelta,
		NotifyItemCompleted,
	}, sink.methods())
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewReasoningItem("item-1", []string{"Let me think."}), completed.Item)
}

func TestItemStreamIgnoresWhatIsNotAnItem(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	spans := []sdktrace.ReadOnlySpan{
		// The user's own prompt: the server emits that item itself.
		newTestSpan("LLM prompt",
			attribute.String(genAIAgentIDAttr, "agent-1"),
			attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser)).running(),
		// Another agent's reply.
		newTestSpan("LLM response", agentAttrs("agent-2")...).running(),
		// A synchronous LLM call with no agent identity.
		newTestSpan("LLM response", attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant)).running(),
		// The system prompt.
		newTestSpan("LLM prompt", append(agentAttrs("agent-1"), attribute.Bool(telemetry.UIInternalAttr, true))...).running(),
		// The failed loop's error carrier.
		newTestSpan(agentFailureSpanName, agentAttrs("agent-1")...).running(),
		// A tool's execution span.
		newTestSpan("Container.withExec", attribute.String(telemetry.LLMToolAttr, "Container.withExec")).running(),
	}
	require.NoError(t, s.ExportSpans(ctx, spans))
	for _, span := range spans {
		require.NoError(t, s.Export(ctx, []sdklog.Record{stdout(span.SpanContext().SpanID(), "noise")}))
	}
	require.Empty(t, sink.methods())

	// stderr on an item span is diagnostics, not content.
	span := newTestSpan("LLM response", agentAttrs("agent-1")...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{stderr(span.id, "warning"), markdown(span.id, "text"), eof(span.id, 1)}))
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()}))
	s.EndTurn(ref, true, 0, time.Second)
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewAgentMessageItem("item-1", "text"), completed.Item)
}

func TestItemStreamEndTurnForcesOpenItemsAndDropsLateSpans(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	span := newTestSpan("LLM response", agentAttrs("agent-1")...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()}))
	require.NoError(t, s.Export(ctx, []sdklog.Record{markdown(span.id, "partial")}))

	start := time.Now()
	saw := s.EndTurn(ref, true, 0, 100*time.Millisecond)
	require.True(t, saw)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, []string{NotifyItemStarted, NotifyAgentMessageDelta, NotifyItemCompleted}, sink.methods())
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewAgentMessageItem("item-1", "partial"), completed.Item)

	// Once the turn is over, its agent's spans are nobody's: no resolver
	// match means no item.
	s.SetResolver(func(string) (TurnRef, bool) { return TurnRef{}, false })
	late := newTestSpan("LLM response", agentAttrs("agent-1")...)
	require.NoError(t, s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{late.running(), late.ended()}))
	require.Len(t, sink.methods(), 3)
}

func TestItemStreamEndTurnWaitsForTheReply(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	ref := TurnRef{ThreadID: "t1", TurnID: "u1"}
	s := newTestStream(t, sink, ref, "agent-1")

	// The reply's telemetry lands shortly after the turn is reported done.
	go func() {
		time.Sleep(50 * time.Millisecond)
		span := newTestSpan("LLM response", agentAttrs("agent-1")...)
		_ = s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()})
		_ = s.Export(ctx, []sdklog.Record{markdown(span.id, "late reply"), eof(span.id, 1)})
		_ = s.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()})
	}()
	saw := s.EndTurn(ref, true, 10*time.Millisecond, 2*time.Second)
	require.True(t, saw)
	completed := sink.find(NotifyItemCompleted, 0).(ItemCompletedNotification)
	require.Equal(t, NewAgentMessageItem("item-1", "late reply"), completed.Item)

	// With nothing coming, the wait gives up at the timeout.
	ref2 := TurnRef{ThreadID: "t1", TurnID: "u2"}
	s.BeginTurn(ref2)
	start := time.Now()
	require.False(t, s.EndTurn(ref2, true, 0, 100*time.Millisecond))
	require.Less(t, time.Since(start), time.Second)
}
