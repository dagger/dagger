package core

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

const (
	agentTraceID    = "000102030405060708090a0b0c0d0e0f"
	importedTraceID = "0f0e0d0c0b0a09080706050403020100"

	agentRootSpan     = "0000000000000001"
	agentChiefSpan    = "0000000000000002"
	agentSpawnSpan    = "0000000000000003"
	agentWorkerSpan   = "0000000000000004"
	agentSummarySpan  = "0000000000000005"
	agentSummaryCall  = "0000000000000006"
	agentLoneSpan     = "0000000000000007"
	agentLoneCall     = "0000000000000008"
	agentWorkerExec   = "0000000000000009"
	agentWorkerRounds = 30
)

// conversationBuilder builds a recorded LLM conversation the way the engine
// records it: a receiver chain of LLM selectors rooted at llm.
type conversationBuilder struct {
	t  *testing.T
	id *call.ID
}

func newConversation(t *testing.T) *conversationBuilder {
	return &conversationBuilder{t: t, id: call.New().Append(&ast.Type{NamedType: "LLM", NonNull: true}, "llm")}
}

func (c *conversationBuilder) step(field string, args ...any) *conversationBuilder {
	var callArgs []*call.Argument
	for i := 0; i < len(args); i += 2 {
		lit, err := call.ToLiteral(args[i+1])
		require.NoError(c.t, err)
		callArgs = append(callArgs, call.NewArgument(args[i].(string), lit, false))
	}
	c.id = c.id.Append(&ast.Type{NamedType: "LLM", NonNull: true}, field, call.WithArgs(callArgs...))
	return c
}

func (c *conversationBuilder) prompt(text string) *conversationBuilder {
	return c.step("withPrompt", "prompt", text)
}

func (c *conversationBuilder) reply(text string) *conversationBuilder {
	return c.step("withResponse", "content", []any{map[string]any{"kind": "TEXT", "text": text}})
}

func (c *conversationBuilder) toolCall(tool, callID, arguments string) *conversationBuilder {
	return c.step("withResponse", "content", []any{map[string]any{"kind": "TOOL_CALL", "toolName": tool, "callId": callID, "arguments": arguments}})
}

func (c *conversationBuilder) toolResult(callID, text string) *conversationBuilder {
	return c.step("withToolResult", "callId", callID, "content", text)
}

func (c *conversationBuilder) digest() string { return c.id.Digest().String() }

// payloadLogs are the conversation's frames as call-payload log records,
// minus the digests in skip (frames that ride a span instead).
func (c *conversationBuilder) payloadLogs(traceID, spanID string, skip ...string) []clientdb.Log {
	dag, err := c.id.ToProto()
	require.NoError(c.t, err)
	var logs []clientdb.Log
	for digest, frame := range dag.GetRecipe().CallsByDigest {
		if containsString(skip, digest) {
			continue
		}
		payload, err := proto.Marshal(frame)
		require.NoError(c.t, err)
		body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
		require.NoError(c.t, err)
		logs = append(logs, clientdb.Log{
			TraceID: validSpanID(traceID), SpanID: validSpanID(spanID), Body: body,
			Attributes: marshalSpanAttrs(c.t, strAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType)),
			Resource:   []byte("{}"), InstrumentationScope: []byte("{}"),
		})
	}
	return logs
}

// tipAttrs are the attributes of a span that carries the conversation tip's
// frame, as a traced LLM call does.
func (c *conversationBuilder) tipAttrs() []*otlpcommonv1.KeyValue {
	dag, err := c.id.ToProto()
	require.NoError(c.t, err)
	encoded, err := dag.GetRecipe().CallsByDigest[c.digest()].Encode()
	require.NoError(c.t, err)
	return []*otlpcommonv1.KeyValue{strAttr(telemetry.DagDigestAttr, c.digest()), strAttr(telemetry.DagCallAttr, encoded)}
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func strAttr(key, value string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}}
}

func agentAttrs(id, name string) []*otlpcommonv1.KeyValue {
	return []*otlpcommonv1.KeyValue{
		boolAttr(telemetryattrs.AgentAttr),
		strAttr(telemetryattrs.AgentIDAttr, id),
		strAttr(telemetryattrs.AgentNameAttr, name),
	}
}

func controlLog(t *testing.T, a agentcontrol.Agent) clientdb.Log {
	t.Helper()
	rec := a.Record()
	var attrs []*otlpcommonv1.KeyValue
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		attrs = append(attrs, &otlpcommonv1.KeyValue{Key: kv.Key, Value: telemetry.LogValueToPB(kv.Value)})
		return true
	})
	encoded, err := clientdb.MarshalProtoJSONs(attrs)
	require.NoError(t, err)
	body, err := proto.Marshal(telemetry.LogValueToPB(rec.Body()))
	require.NoError(t, err)
	return clientdb.Log{TraceID: validSpanID(a.Trace), Attributes: encoded, Body: body, Resource: []byte("{}"), InstrumentationScope: []byte("{}")}
}

func finishSpanRows(rows []clientdb.Span) []clientdb.Span {
	for i := range rows {
		rows[i].StartTime = int64(100 + i)
		rows[i].EndTime = sql.NullInt64{Int64: 1000, Valid: true}
		if rows[i].Attributes == nil {
			rows[i].Attributes = []byte("[]")
		}
		rows[i].Links = []byte("[]")
		rows[i].Events = []byte("[]")
		rows[i].Resource = []byte("{}")
		rows[i].InstrumentationScope = []byte("{}")
	}
	return rows
}

// agentTraceStore is a session with a chief agent that spawned a worker
// from a staff_spawn tool call, and ran a module tool whose function had its
// own (non-agent) LLM conversation; plus an imported trace holding an agent
// with no control record, whose conversation must be inferred.
func agentTraceStore(t *testing.T) (*clientdb.DB, *clientdb.DBs) {
	t.Helper()
	dbs := clientdb.NewDBs(t.TempDir())
	// The name logCaptureTestServer opens, so dispatch tests share it.
	store, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	chief := newConversation(t).prompt("chief task: fix the bug").
		toolCall("staff_spawn", "call-1", `{"task":"find the bug"}`).toolResult("call-1", "spawned worker").
		reply("all done: the bug is fixed")
	worker := newConversation(t).prompt("worker task: find the bug")
	for i := range agentWorkerRounds {
		id := fmt.Sprintf("call-w%d", i)
		worker.toolCall("Grep", id, fmt.Sprintf(`{"pattern":"bug%d"}`, i)).
			toolResult(id, fmt.Sprintf("main.go:%d: the bug%d is here", i, i))
	}
	worker.reply("worker final reply: the bug is in main.go")
	summary := newConversation(t).prompt("summarize this page").reply("the page, summarized")

	llmTool := func(name string) *otlpcommonv1.KeyValue { return strAttr(telemetry.LLMToolAttr, name) }
	spans := []clientdb.Span{
		{TraceID: agentTraceID, SpanID: agentRootSpan, Name: "session"},
		{TraceID: agentTraceID, SpanID: agentChiefSpan, ParentSpanID: validSpanID(agentRootSpan), Name: "agent: chief",
			Attributes: marshalSpanAttrs(t, agentAttrs("chief-h", "chief")...)},
		{TraceID: agentTraceID, SpanID: agentSpawnSpan, ParentSpanID: validSpanID(agentChiefSpan), Name: "staff_spawn",
			Attributes: marshalSpanAttrs(t, llmTool("staff_spawn"))},
		{TraceID: agentTraceID, SpanID: agentWorkerSpan, ParentSpanID: validSpanID(agentSpawnSpan), Name: "agent: worker",
			Attributes: marshalSpanAttrs(t, agentAttrs("worker-h", "worker")...)},
		{TraceID: agentTraceID, SpanID: agentSummarySpan, ParentSpanID: validSpanID(agentChiefSpan), Name: "summarize",
			Attributes: marshalSpanAttrs(t, llmTool("summarize"))},
		{TraceID: agentTraceID, SpanID: agentSummaryCall, ParentSpanID: validSpanID(agentSummarySpan), Name: "LLM.withResponse",
			Attributes: marshalSpanAttrs(t, summary.tipAttrs()...)},
		{TraceID: agentTraceID, SpanID: agentWorkerExec, ParentSpanID: validSpanID(agentWorkerSpan), Name: "Container.withExec"},
	}
	_, err = store.AppendSpans(finishSpanRows(spans))
	require.NoError(t, err)
	ns := agentcontrol.Namespace{Session: "session", Trace: agentTraceID, Incarnation: "incarnation"}
	logs := append(chief.payloadLogs(agentTraceID, agentChiefSpan), worker.payloadLogs(agentTraceID, agentWorkerSpan)...)
	logs = append(logs, summary.payloadLogs(agentTraceID, agentSummaryCall, summary.digest())...)
	logs = append(logs,
		controlLog(t, agentcontrol.Agent{Key: agentcontrol.Key{Namespace: ns, Handle: "chief-h"}, Revision: 1, Name: "chief", State: "IDLE", Digest: chief.digest()}),
		controlLog(t, agentcontrol.Agent{Key: agentcontrol.Key{Namespace: ns, Handle: "worker-h"}, Revision: 1, Name: "worker", Parent: "chief-h", State: "IDLE", Digest: "xxh3:stale"}),
		controlLog(t, agentcontrol.Agent{Key: agentcontrol.Key{Namespace: ns, Handle: "worker-h"}, Revision: 2, Name: "worker", Parent: "chief-h", State: "IDLE", Digest: worker.digest()}),
	)
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)

	lone := newConversation(t).prompt("lone prompt").reply("lone reply")
	_, err = store.ImportTrace(t.Context(), importedTraceID, func(imported *clientdb.DB) error {
		if _, err := imported.AppendSpans(finishSpanRows([]clientdb.Span{
			{TraceID: importedTraceID, SpanID: agentLoneSpan, Name: "agent: lone", Attributes: marshalSpanAttrs(t, agentAttrs("lone-h", "lone")...)},
			{TraceID: importedTraceID, SpanID: agentLoneCall, ParentSpanID: validSpanID(agentLoneSpan), Name: "LLM.withResponse",
				Attributes: marshalSpanAttrs(t, lone.tipAttrs()...)},
		})); err != nil {
			return err
		}
		_, err := imported.AppendLogs(lone.payloadLogs(importedTraceID, agentLoneCall, lone.digest()))
		return err
	})
	require.NoError(t, err)
	return store, dbs
}

func TestReadTraceAgents(t *testing.T) {
	store, _ := agentTraceStore(t)
	got, err := readTraceAgentsIn(t.Context(), store, "")
	require.NoError(t, err)
	require.Contains(t, got, "3 agent(s); read one with ReadTrace(view: \"transcript\", agent: <handle or unique name>)")
	require.Contains(t, got, "chief-h  chief  IDLE  span="+agentChiefSpan+"\n  worker-h  worker  IDLE  span="+agentWorkerSpan+"\n")
	require.Contains(t, got, "lone-h  lone  -  span="+agentLoneSpan+"  (no checkpoint; transcript inferred best-effort)\n")

	// A span scopes the roster to its subtree, and names the agent it is in.
	scoped, err := readTraceAgentsIn(t.Context(), store, agentSpawnSpan)
	require.NoError(t, err)
	require.Contains(t, scoped, "Agents at or beneath span "+agentSpawnSpan+".")
	require.Contains(t, scoped, "inside agent chief-h (chief), span="+agentChiefSpan)
	require.Contains(t, scoped, "worker-h  worker  IDLE  span="+agentWorkerSpan+"  (parent agent chief-h)\n")
	require.NotContains(t, scoped, "lone-h")
	none, err := readTraceAgentsIn(t.Context(), store, agentSummaryCall)
	require.NoError(t, err)
	require.Contains(t, none, "(no agents)")

	_, err = readTraceAgentsIn(t.Context(), store, "00000000000000ff")
	require.ErrorContains(t, err, "no span")
}

func TestReadTraceTranscript(t *testing.T) {
	store, _ := agentTraceStore(t)
	read := func(opts transcriptOpts) string {
		t.Helper()
		if opts.Limit == 0 {
			opts.Limit = transcriptDefaultLimit
		}
		if opts.Clip == 0 {
			opts.Clip = transcriptDefaultClip
		}
		got, err := readTraceTranscriptIn(t.Context(), store, opts)
		require.NoError(t, err)
		return got
	}
	workerMessages := 2 + 2*agentWorkerRounds // prompt, rounds, final reply

	// By name: a page of messages, tool results verbatim, and the exact call
	// that reads the next page.
	page := read(transcriptOpts{Agent: "worker", Limit: 3})
	require.Contains(t, page, "agent span="+agentWorkerSpan)
	require.Contains(t, page, "agent worker (worker-h) · IDLE · recorded-checkpoint ")
	require.Contains(t, page, fmt.Sprintf("messages 1-3 of %d matching (all non-system messages)", workerMessages))
	require.Contains(t, page, "── #0 USER ──\nworker task: find the bug\n")
	require.Contains(t, page, "→ Grep  [call-w0]\n  pattern: bug0\n")
	require.Contains(t, page, "── #2 TOOL RESULT ──\n← Grep  [call-w0]\nmain.go:0: the bug0 is here\n")
	require.Contains(t, page, fmt.Sprintf(`next page: ReadTrace(view: "transcript", agent: "worker-h", offset: 3, limit: 3) (%d more)`, workerMessages-3))
	require.NotContains(t, page, "#3 ")

	last := read(transcriptOpts{Agent: "worker-h", Offset: workerMessages - 1})
	require.Contains(t, last, "worker final reply")
	require.NotContains(t, last, "next page")

	// Filters keep their place in the next-page call.
	filtered := read(transcriptOpts{Agent: "worker-h", Role: "tool", Grep: "bug1", Limit: 2})
	require.Contains(t, filtered, `messages 1-2 of 11 matching (role="tool", grep="bug1")`)
	require.Contains(t, filtered, `ReadTrace(view: "transcript", agent: "worker-h", role: "tool", grep: "bug1", offset: 2, limit: 2)`)
	require.NotContains(t, filtered, "→ Grep")
	clipped := read(transcriptOpts{Agent: "worker-h", Tool: "Grep", Clip: 8, Limit: 2})
	require.Contains(t, clipped, "main.go:\n[... ")
	require.Contains(t, clipped, `clip: 8`)

	// A span resolves to the agent it names, the agent beneath it, or the
	// agent enclosing it.
	for span, want := range map[string]string{
		agentWorkerSpan: "agent worker (worker-h)",
		agentSpawnSpan:  "resolved to the agent beneath span " + agentSpawnSpan,
		agentWorkerExec: "resolved to the agent enclosing span " + agentWorkerExec,
		agentChiefSpan:  "agent chief (chief-h)",
		agentRootSpan:   "agent chief (chief-h)",
	} {
		require.Contains(t, read(transcriptOpts{Span: span}), want, span)
	}
	chief := read(transcriptOpts{Span: agentChiefSpan})
	require.Contains(t, chief, "chief task: fix the bug")
	require.Contains(t, chief, "all done: the bug is fixed")

	// A tool call whose module function ran its own LLM loop reads that
	// conversation, inferred from the calls traced beneath it.
	summary := read(transcriptOpts{Span: agentSummarySpan})
	require.Contains(t, summary, "resolved to the LLM conversation beneath span "+agentSummarySpan+" (not an agent)")
	require.Contains(t, summary, "summarize this page")
	require.Contains(t, summary, "the page, summarized")
	require.Contains(t, summary, "WARNING: no checkpoint names this conversation")

	// An imported agent with no control record is inferred from its spans.
	lone := read(transcriptOpts{Agent: "lone"})
	require.Contains(t, lone, "lone prompt")
	require.Contains(t, lone, "lone reply")
	require.Contains(t, lone, "inferred from traced calls")
	require.Contains(t, lone, "GAP")

	for _, tc := range []struct {
		opts transcriptOpts
		want string
	}{
		{transcriptOpts{Limit: 20}, "needs agent"},
		{transcriptOpts{Agent: "nobody", Limit: 20}, `no agent "nobody"`},
		{transcriptOpts{Agent: "worker", Limit: 0}, "invalid limit"},
		{transcriptOpts{Agent: "worker", Limit: 101}, "invalid limit"},
		{transcriptOpts{Agent: "worker", Limit: 20, Role: "robot"}, "invalid role"},
		{transcriptOpts{Agent: "worker", Limit: 20, Grep: "["}, "invalid grep"},
		{transcriptOpts{Agent: "worker", Limit: 20, Clip: -1}, "invalid clip"},
		{transcriptOpts{Span: "not-a-span", Limit: 20}, "invalid span ID"},
	} {
		_, err := readTraceTranscriptIn(t.Context(), store, tc.opts)
		require.ErrorContains(t, err, tc.want, "%+v", tc.opts)
	}
}

func TestReadTraceAgentViewsThroughDispatch(t *testing.T) {
	_, dbs := agentTraceStore(t)
	srv := &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}
	ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
	m := newMCP()
	tools := NewLLMToolSet()
	m.loadBuiltins(&dagql.Server{}, tools)
	call := func(args string) *LLMContentBlock {
		return m.CallContent(ctx, tools.Order, &LLMToolCall{Name: "ReadTrace", Arguments: JSON(args)})
	}
	agents := call(`{"view":"agents"}`)
	require.False(t, agents.Errored, agents.Text)
	require.Contains(t, agents.Text, "worker-h  worker  IDLE")
	page := call(`{"view":"transcript","agent":"worker"}`)
	require.False(t, page.Errored, page.Text)
	require.Contains(t, page.Text, fmt.Sprintf("messages 1-20 of %d", 2+2*agentWorkerRounds), "transcript pages default to 20 messages")
	require.Contains(t, page.Text, `offset: 20)`)
	bad := call(`{"view":"transcript","agent":"worker","limit":500}`)
	require.True(t, bad.Errored)
	require.Contains(t, bad.Text, "invalid limit")
	report := call(`{"span":"` + agentWorkerSpan + `"}`)
	require.False(t, report.Errored, report.Text)
}

func TestConversationSummary(t *testing.T) {
	store, _ := agentTraceStore(t)
	got := conversationSummary(t.Context(), store, agentSpawnSpan, 61)
	require.Contains(t, got, "== CONVERSATION ==\n61 messages surfaced beneath this span, nested agents' included; omitted here.\n")
	require.Contains(t, got, fmt.Sprintf("Transcript of agent worker (worker-h) IDLE: %d messages (1 prompt, %d replies, %d tool results).", 2+2*agentWorkerRounds, agentWorkerRounds+1, agentWorkerRounds))
	require.Contains(t, got, "Opening prompt (#0):\n  worker task: find the bug\n")
	require.Contains(t, got, "Final reply (#61):\n  worker final reply: the bug is in main.go\n")
	require.Contains(t, got, fmt.Sprintf(`ReadTrace(span: %q, view: "transcript")`, agentSpawnSpan))
	require.NotContains(t, got, "may still have been running", "it ends with its final reply")

	// Without a decodable transcript, the size and the pointer remain.
	bare := conversationSummary(t.Context(), store, agentWorkerExec+"x", 40)
	require.Contains(t, bare, "40 messages surfaced")
	require.Contains(t, bare, `view: "transcript"`)
	require.NotContains(t, bare, "Opening prompt")
}

// A ReadTrace report on an agent's span summarizes a long conversation
// instead of rendering it inline; a short one still renders.
func TestReadTraceReportSummarizesLongConversation(t *testing.T) {
	start := time.Unix(100, 0)
	build := func(messages int) (*idtui.ReportSession, dagui.SpanSnapshot) {
		root := dagui.SpanSnapshot{
			ID: traceTargetSpanID(1), TraceID: dagui.TraceID{TraceID: trace.TraceID{1}},
			Name: "agent: worker", StartTime: start, EndTime: start.Add(time.Hour), Final: true,
		}
		snaps := []dagui.SpanSnapshot{root}
		var records []sdklog.Record
		for i := range messages {
			msg := dagui.SpanSnapshot{
				ID: traceTargetSpanID(byte(i + 2)), ParentID: root.ID, TraceID: root.TraceID,
				Name: "LLM response", LLMRole: "assistant",
				StartTime: start.Add(time.Duration(i+1) * time.Second), EndTime: start.Add(time.Duration(i+2) * time.Second), Final: true,
			}
			snaps = append(snaps, msg)
			var rec sdklog.Record
			rec.SetSpanID(msg.ID.SpanID)
			rec.SetBody(otellog.StringValue(fmt.Sprintf("MESSAGE-%d\n", i)))
			records = append(records, rec)
		}
		db := dagui.NewDB()
		db.ImportSnapshots(snaps)
		session := idtui.NewReportSession(db)
		require.NoError(t, session.LogExporter().Export(t.Context(), records))
		return session, root
	}

	session, root := build(traceReportInlineMessages)
	short, err := renderTraceReportSession(session, root.ID.String(), readTraceReportOpts())
	require.NoError(t, err)
	require.Zero(t, short.omittedMessages)
	require.Contains(t, short.body, "CONVERSATION")
	require.Contains(t, short.body, "MESSAGE-0")

	session, root = build(traceReportInlineMessages + 1)
	long, err := renderTraceReportSession(session, root.ID.String(), readTraceReportOpts())
	require.NoError(t, err)
	require.Equal(t, traceReportInlineMessages+1, long.omittedMessages)
	require.NotContains(t, long.body, "MESSAGE-0")
	require.NotContains(t, long.body, "sub-agent conversation omitted", "the summary, not the tool-result pointer, stands in")

	// A tool call's own result keeps its one-line pointer.
	session, root = build(traceReportInlineMessages + 1)
	tool, err := renderTraceReportSession(session, root.ID.String(), toolCallReportOpts())
	require.NoError(t, err)
	require.Zero(t, tool.omittedMessages)
	require.Contains(t, tool.body, subAgentConversationPointer(root.ID.String()))
	require.Contains(t, tool.body, `view: "transcript"`)
}
