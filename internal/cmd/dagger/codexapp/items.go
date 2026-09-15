package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// The item stream turns the engine's LLM telemetry into protocol items.
//
// A Dagger agent narrates its turn as OTel spans and log records: one span per
// assistant content block (an "LLM response" text span, a "thinking" span, a
// tool-call span named after the tool) with the block's text streamed as log
// records beneath it; a tool-call span's stdout carries the call's arguments
// and then the result the model saw, each closed by a stdio EOF record
// (core/llm_display.go, core/mcp.go). The CLI already ingests that
// stream to draw the TUI; this tees it and emits the Codex equivalents:
// agentMessage, reasoning and mcpToolCall items, started when their span
// starts, streamed as deltas, and completed when the span ends.
//
// Spans and logs arrive on separate pipelines with no ordering guarantee
// between them, so a log may precede its span (buffered until the span shows
// up) or trail the span's end (the item waits for its stdio EOF record, or a
// short grace period, before completing). The stream never reads from the
// engine: everything it knows, it learned from the wire.

// genAIAgentIDAttr is the OTel GenAI semantic-convention attribute the engine
// stamps on every conversation message span an agent runtime emits: the
// spawn-minted runtime handle. It is how a span is attributed to a thread.
const genAIAgentIDAttr = "gen_ai.agent.id"

// llmThinkingAttr marks a thinking span (core/llm_display.go).
const llmThinkingAttr = "llm.thinking"

// agentFailureSpanName is the span the engine emits beneath a failed loop
// carrying the terminal error (core/agent_telemetry.go). It is assistant-role
// so the TUI keeps it in scrollback, but it is not a message.
const agentFailureSpanName = "agent failure"

// TurnRef identifies a turn within a thread.
type TurnRef struct {
	ThreadID string
	TurnID   string
}

// Sink receives the notifications the stream emits.
type Sink interface {
	Notify(method string, params any) error
}

// TurnResolver maps an agent runtime handle to the turn it is currently
// serving. Spans whose agent has no active turn are not this stream's
// business (a sub-agent's, or an idle thread's replay).
type TurnResolver func(agentID string) (TurnRef, bool)

type spanKind int

const (
	kindAgentMessage spanKind = iota + 1
	kindReasoning
	kindToolCall
)

type trackedSpan struct {
	kind  spanKind
	ref   TurnRef
	item  *item
	ended bool
}

type item struct {
	id        string
	kind      spanKind
	ref       TurnRef
	startedAt time.Time

	// text is the message/thinking text, or a tool call's JSON arguments.
	text strings.Builder
	// result is a tool call's output, streamed after its arguments.
	result strings.Builder

	tool, server string

	started   bool // item/started emitted
	completed bool // item/completed emitted
	spanEnded bool
	endedAt   time.Time
	failed    bool
	errMsg    string
	// argsDone records that a tool call's arguments stream closed, so what
	// follows on its stdout is the result.
	argsDone bool
	// eof records that the item's content stream closed: the message text,
	// or a tool call's result.
	eof bool
	// graceTimer fires when a span that ended without an EOF has waited long
	// enough for trailing records.
	graceTimer *time.Timer
}

type turnItems struct {
	open            int
	sawAgentMessage bool
	lastEvent       time.Time
}

// ItemStream is an OTel span and log exporter that emits protocol items.
type ItemStream struct {
	sink    Sink
	resolve TurnResolver
	now     func() time.Time
	newID   func() string
	grace   time.Duration

	mu           sync.Mutex
	cond         *sync.Cond
	spans        map[trace.SpanID]*trackedSpan
	turns        map[TurnRef]*turnItems
	pending      map[trace.SpanID][]sdklog.Record
	pendingOrder []trace.SpanID
}

// maxPendingSpans bounds how many not-yet-seen spans have logs buffered for
// them. Most spans in a trace are never items, so the buffer is a small
// reorder window, not a store.
const maxPendingSpans = 2048

// defaultGrace is how long an ended span waits for its trailing records.
const defaultGrace = 750 * time.Millisecond

// NewItemStream returns a stream emitting to sink. The resolver is set by the
// server that owns the stream (SetResolver) once it exists; until then every
// span is unattributed and ignored.
func NewItemStream(sink Sink) *ItemStream {
	s := &ItemStream{
		sink:    sink,
		resolve: func(string) (TurnRef, bool) { return TurnRef{}, false },
		now:     time.Now,
		newID:   NewID,
		grace:   defaultGrace,
		spans:   map[trace.SpanID]*trackedSpan{},
		turns:   map[TurnRef]*turnItems{},
		pending: map[trace.SpanID][]sdklog.Record{},
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// SetResolver installs the agent-to-turn mapping.
func (s *ItemStream) SetResolver(resolve TurnResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolve = resolve
}

// BeginTurn starts attributing the agent's spans to ref.
func (s *ItemStream) BeginTurn(ref TurnRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns[ref] = &turnItems{lastEvent: s.now()}
}

// EndTurn waits for the turn's items to settle, then stops attributing spans
// to it. Settled means every started item has completed and the stream has
// been quiet for `quiet`; when expectReply is set (the turn ended with a
// reply) it also means an agent message was seen, since the reply's span may
// still be in flight when the engine reports the turn done. The wait is
// bounded by timeout, after which anything still open is completed with what
// arrived. It reports whether an agent message was seen, so the caller can
// fall back to the reply it fetched itself.
func (s *ItemStream) EndTurn(ref TurnRef, expectReply bool, quiet, timeout time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, ok := s.turns[ref]
	if !ok {
		return false
	}
	deadline := s.now().Add(timeout)
	for {
		now := s.now()
		settled := ti.open == 0 &&
			(!expectReply || ti.sawAgentMessage) &&
			now.Sub(ti.lastEvent) >= quiet
		if settled || !now.Before(deadline) {
			break
		}
		wake := time.AfterFunc(50*time.Millisecond, func() {
			s.mu.Lock()
			s.cond.Broadcast()
			s.mu.Unlock()
		})
		s.cond.Wait()
		wake.Stop()
	}
	for id, ts := range s.spans {
		if ts.ref != ref {
			continue
		}
		if ts.item != nil && !ts.item.completed {
			s.complete(ts.item)
		}
		delete(s.spans, id)
	}
	saw := ti.sawAgentMessage
	delete(s.turns, ref)
	return saw
}

// ExportSpans implements sdktrace.SpanExporter.
func (s *ItemStream) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, span := range spans {
		s.observeSpan(span)
	}
	return nil
}

// Shutdown implements sdktrace.SpanExporter and sdklog.Exporter.
func (s *ItemStream) Shutdown(context.Context) error { return nil }

// ForceFlush implements sdktrace.SpanExporter and sdklog.Exporter.
func (s *ItemStream) ForceFlush(context.Context) error { return nil }

// Export implements sdklog.Exporter.
func (s *ItemStream) Export(_ context.Context, records []sdklog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range records {
		s.observeLog(&records[i])
	}
	return nil
}

func spanEnded(span sdktrace.ReadOnlySpan) bool {
	end := span.EndTime()
	return !end.IsZero() && !end.Before(span.StartTime())
}

func (s *ItemStream) observeSpan(span sdktrace.ReadOnlySpan) {
	id := span.SpanContext().SpanID()
	ts, known := s.spans[id]
	if !known {
		ts = s.classify(span)
		if ts == nil {
			return
		}
		s.spans[id] = ts
		s.touch(ts.ref)
		s.start(ts.item)
		for _, rec := range s.pending[id] {
			s.applyLog(ts, &rec)
		}
		delete(s.pending, id)
	}
	if spanEnded(span) && !ts.ended {
		ts.ended = true
		s.touch(ts.ref)
		it := ts.item
		it.spanEnded = true
		it.endedAt = span.EndTime()
		if status := span.Status(); status.Code == codes.Error {
			it.failed = true
			it.errMsg = status.Description
		}
		s.maybeComplete(it)
	}
}

// classify decides what a span is to this stream, or nil when it is nothing.
func (s *ItemStream) classify(span sdktrace.ReadOnlySpan) *trackedSpan {
	var agentID, role, tool, server string
	var thinking, internal bool
	for _, kv := range span.Attributes() {
		switch string(kv.Key) {
		case genAIAgentIDAttr:
			agentID = kv.Value.AsString()
		case telemetry.LLMRoleAttr:
			role = kv.Value.AsString()
		case telemetry.LLMToolAttr:
			tool = kv.Value.AsString()
		case telemetry.LLMToolServerAttr:
			server = kv.Value.AsString()
		case llmThinkingAttr:
			thinking = kv.Value.AsBool()
		case telemetry.UIInternalAttr:
			internal = kv.Value.AsBool()
		}
	}
	// Only an agent's own assistant blocks are items. A tool's execution
	// spans carry no agent identity, and a sub-agent's messages carry its.
	if agentID == "" || role != telemetry.LLMRoleAssistant || internal || span.Name() == agentFailureSpanName {
		return nil
	}
	ref, ok := s.resolve(agentID)
	if !ok {
		return nil
	}
	it := &item{
		id:        s.newID(),
		ref:       ref,
		startedAt: span.StartTime(),
	}
	switch {
	case tool != "":
		it.kind = kindToolCall
		it.tool = tool
		it.server = server
		if it.server == "" {
			it.server = DaggerToolServer
		}
	case thinking:
		it.kind = kindReasoning
	default:
		it.kind = kindAgentMessage
	}
	return &trackedSpan{kind: it.kind, ref: ref, item: it}
}

func (s *ItemStream) touch(ref TurnRef) {
	if ti, ok := s.turns[ref]; ok {
		ti.lastEvent = s.now()
	}
}

// start emits item/started for a message or reasoning item. Tool calls wait
// for their arguments (startToolCall), which stream in after the span opens.
func (s *ItemStream) start(it *item) {
	ti, ok := s.turns[it.ref]
	if !ok {
		return
	}
	ti.open++
	switch it.kind {
	case kindAgentMessage:
		ti.sawAgentMessage = true
		it.started = true
		s.notifyStarted(it, NewAgentMessageItem(it.id, ""))
	case kindReasoning:
		it.started = true
		s.notifyStarted(it, NewReasoningItem(it.id, nil))
		s.notify(NotifyReasoningSummaryPart, ReasoningSummaryPartAddedNotification{
			ThreadID: it.ref.ThreadID, TurnID: it.ref.TurnID, ItemID: it.id, SummaryIndex: 0,
		})
	case kindToolCall:
		// Started once the arguments are complete.
	}
}

func (s *ItemStream) startToolCall(it *item) {
	if it.started {
		return
	}
	it.started = true
	s.notifyStarted(it, s.toolCallItem(it, ToolCallStatusInProgress))
}

func (s *ItemStream) observeLog(rec *sdklog.Record) {
	id := rec.SpanID()
	ts, ok := s.spans[id]
	if !ok {
		s.bufferLog(id, rec)
		return
	}
	s.applyLog(ts, rec)
}

// bufferLog holds a record for a span that has not been seen yet, in case it
// turns out to be an item. The buffer is a bounded reorder window: once full,
// the oldest span's records are dropped.
func (s *ItemStream) bufferLog(id trace.SpanID, rec *sdklog.Record) {
	if _, ok := s.pending[id]; !ok {
		if len(s.pendingOrder) >= maxPendingSpans {
			oldest := s.pendingOrder[0]
			s.pendingOrder = s.pendingOrder[1:]
			delete(s.pending, oldest)
		}
		s.pendingOrder = append(s.pendingOrder, id)
	}
	s.pending[id] = append(s.pending[id], rec.Clone())
}

func (s *ItemStream) applyLog(ts *trackedSpan, rec *sdklog.Record) {
	var stream int64
	var eof bool
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		switch kv.Key {
		case telemetry.StdioStreamAttr:
			stream = kv.Value.AsInt64()
		case telemetry.StdioEOFAttr:
			eof = kv.Value.AsBool()
		}
		return true
	})
	if stream == 2 {
		// stderr is diagnostics, not content.
		return
	}
	it := ts.item
	if it == nil || it.completed {
		return
	}
	s.touch(it.ref)
	body := rec.Body().AsString()
	switch ts.kind {
	case kindAgentMessage:
		if eof {
			it.eof = true
			s.maybeComplete(it)
			return
		}
		if body == "" {
			return
		}
		it.text.WriteString(body)
		s.notify(NotifyAgentMessageDelta, AgentMessageDeltaNotification{
			ThreadID: it.ref.ThreadID, TurnID: it.ref.TurnID, ItemID: it.id, Delta: body,
		})
	case kindReasoning:
		if eof {
			it.eof = true
			s.maybeComplete(it)
			return
		}
		if body == "" {
			return
		}
		it.text.WriteString(body)
		s.notify(NotifyReasoningSummaryDelta, ReasoningSummaryTextDeltaNotification{
			ThreadID: it.ref.ThreadID, TurnID: it.ref.TurnID, ItemID: it.id, SummaryIndex: 0, Delta: body,
		})
	case kindToolCall:
		// The call's stdout is its JSON arguments up to the first EOF (the
		// call is underway from then on), then its result up to the second.
		if eof {
			if !it.argsDone {
				it.argsDone = true
				s.startToolCall(it)
				return
			}
			it.eof = true
			s.maybeComplete(it)
			return
		}
		if it.argsDone {
			it.result.WriteString(body)
		} else {
			it.text.WriteString(body)
		}
	}
}

// maybeComplete completes an item whose span has ended once its content
// stream has closed too, or after the grace period if that EOF never shows.
func (s *ItemStream) maybeComplete(it *item) {
	if it.completed || !it.spanEnded {
		return
	}
	if !it.eof {
		if it.graceTimer == nil {
			it.graceTimer = time.AfterFunc(s.grace, func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				if !it.completed {
					s.complete(it)
				}
			})
		}
		return
	}
	s.complete(it)
}

// complete emits item/completed with everything the item accumulated.
func (s *ItemStream) complete(it *item) {
	if it.completed {
		return
	}
	if it.graceTimer != nil {
		it.graceTimer.Stop()
	}
	it.completed = true
	if it.kind == kindToolCall {
		s.startToolCall(it)
	}
	if it.started {
		if ti, ok := s.turns[it.ref]; ok && ti.open > 0 {
			ti.open--
		}
	}
	completedAt := it.endedAt
	if completedAt.IsZero() {
		completedAt = s.now()
	}
	var proto ThreadItem
	switch it.kind {
	case kindAgentMessage:
		proto = NewAgentMessageItem(it.id, it.text.String())
	case kindReasoning:
		proto = NewReasoningItem(it.id, []string{it.text.String()})
	case kindToolCall:
		status := ToolCallStatusCompleted
		if it.failed {
			status = ToolCallStatusFailed
		}
		call := s.toolCallItem(it, status)
		if result := strings.TrimRight(it.result.String(), "\n"); result != "" {
			call.Result = TextResult(result)
		}
		if it.failed {
			msg := it.errMsg
			if msg == "" {
				msg = "tool call failed"
			}
			call.Error = &McpToolCallError{Message: msg}
		}
		if !it.startedAt.IsZero() && completedAt.After(it.startedAt) {
			ms := completedAt.Sub(it.startedAt).Milliseconds()
			call.DurationMs = &ms
		}
		proto = call
	default:
		return
	}
	s.notify(NotifyItemCompleted, ItemCompletedNotification{
		ThreadID:      it.ref.ThreadID,
		TurnID:        it.ref.TurnID,
		Item:          proto,
		CompletedAtMs: completedAt.UnixMilli(),
	})
	s.cond.Broadcast()
}

func (s *ItemStream) toolCallItem(it *item, status string) McpToolCallItem {
	return McpToolCallItem{
		Type:      ItemTypeMcpToolCall,
		ID:        it.id,
		Server:    it.server,
		Tool:      it.tool,
		Arguments: ToolArguments(it.text.String()),
		Status:    status,
	}
}

// ToolArguments presents the streamed argument text as JSON when it is valid
// JSON, and as the raw string otherwise (a call cut off mid-stream).
func ToolArguments(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	return raw
}

func (s *ItemStream) notifyStarted(it *item, proto ThreadItem) {
	startedAt := it.startedAt
	if startedAt.IsZero() {
		startedAt = s.now()
	}
	s.notify(NotifyItemStarted, ItemStartedNotification{
		ThreadID:    it.ref.ThreadID,
		TurnID:      it.ref.TurnID,
		Item:        proto,
		StartedAtMs: startedAt.UnixMilli(),
	})
}

func (s *ItemStream) notify(method string, params any) {
	// A failed write means the client is gone; the server notices on its
	// own read loop, so there is nothing useful to do with the error here.
	_ = s.sink.Notify(method, params)
}
