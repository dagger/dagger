package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/clientdb"
)

// A Dang tool call's result is shaped as a module tool call's: the report
// alone when nothing nested renders, as for a chain of API calls, and the
// report as OUTPUT beside the CHECKS and FAILURES sections when the snippet
// ran checks.
func TestDangSpanResult(t *testing.T) {
	t.Parallel()
	const (
		traceID = "000102030405060708090a0b0c0d0e0f"
		toolID  = "00000000000000e1"
		callID  = "00000000000000e2"
		execID  = "00000000000000e3"
	)
	report := "String! = \"hi\\n\"\nok · 5ms · 1 engine call · workspace scope\n"
	attr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: stringLogBody(value)}
	}
	// result runs the tool call's span with spans beneath it, the report
	// logged to its own.
	result := func(t *testing.T, nested ...clientdb.Span) string {
		t.Helper()
		dbs := clientdb.NewDBs(t.TempDir())
		store, err := dbs.Open(t.Context(), "capture-test")
		require.NoError(t, err)
		defer store.Close()
		spans := append([]clientdb.Span{{TraceID: traceID, SpanID: toolID, Name: "DangEval"}}, nested...)
		for i := range spans {
			spans[i].StartTime = 100
			spans[i].EndTime = sql.NullInt64{Int64: 200, Valid: true}
		}
		_, err = store.AppendSpans(spans)
		require.NoError(t, err)
		_, err = store.AppendLogs([]clientdb.Log{
			persistedCaptureLog(t, traceID, toolID, "stdout", stringLogBody(report)),
			persistedCaptureLog(t, traceID, execID, "stdout", stringLogBody("hi\n")),
		})
		require.NoError(t, err)
		ctx := ContextWithQuery(dangToolCallCtx(t, toolID), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}})
		return newMCP().dangToolResult(ctx, DangReport{Text: report})
	}
	call := clientdb.Span{TraceID: traceID, SpanID: callID, ParentSpanID: validSpanID(toolID), Name: "Container.withExec"}
	exec := clientdb.Span{TraceID: traceID, SpanID: execID, ParentSpanID: validSpanID(callID), Name: "exec echo hi"}
	check := func(name string, failed bool) clientdb.Span {
		span := clientdb.Span{TraceID: traceID, SpanID: callID, ParentSpanID: validSpanID(toolID), Name: "Check.pass",
			Attributes: marshalSpanAttrs(t, attr(telemetry.CheckNameAttr, name))}
		if failed {
			span.StatusCode = int64(codes.Error)
			span.StatusMessage = "the probe failed"
		}
		return span
	}

	t.Run("a chain of API calls shows the report alone", func(t *testing.T) {
		require.Equal(t, strings.TrimSuffix(report, "\n"), result(t, call, exec))
	})

	t.Run("a passing check is reported beside it", func(t *testing.T) {
		got := result(t, check("probe:ok", false))
		require.Contains(t, got, "== OUTPUT ==\n"+strings.TrimSuffix(report, "\n"))
		require.Contains(t, got, "== CHECKS ==")
		require.Contains(t, got, "probe:ok")
		require.Contains(t, got, "use ReadLogs(span: "+toolID+") to read the full logs")
	})

	t.Run("a failing check leads with its failure", func(t *testing.T) {
		got := result(t, check("probe:broken", true))
		require.True(t, strings.HasPrefix(got, "== FAILURES =="), got)
		require.Contains(t, got, `check "probe:broken"`)
		require.Contains(t, got, "== OUTPUT ==\n"+strings.TrimSuffix(report, "\n"))
	})
}

// A DangEval snippet's writes are all or nothing: when one fails to commit,
// the ones before it are undone.
func TestCommitDangWritesAllOrNothing(t *testing.T) {
	t.Parallel()
	workspace := func(address string) dagql.ObjectResult[*Workspace] {
		res, err := dagql.NewResultForCall(&Workspace{Address: address}, &dagql.ResultCall{})
		require.NoError(t, err)
		return dagql.ObjectResult[*Workspace]{Result: res}
	}
	before, after := workspace("before"), workspace("after")

	m := &MCP{mu: &sync.Mutex{}, workspace: before, boundTools: []boundTool{{Owner: "tool"}}}
	writeWS := dangCommit{name: "ws", apply: func() (string, error) {
		m.workspace = after
		m.markStateChanged()
		return "ws written (1 file)\ndiff --git a/x b/x", nil
	}}
	writeTool := dangCommit{name: "tool", apply: func() (string, error) {
		m.mu.Lock()
		m.boundTools[0].Owner = "rebound"
		m.stateChanged = true
		m.mu.Unlock()
		return "tool state updated", nil
	}}
	failing := dangCommit{name: "broken", apply: func() (string, error) {
		return "", errors.New("boom")
	}}

	_, err := m.commitDangWrites([]dangCommit{writeWS, writeTool, failing})
	require.ErrorContains(t, err, "write broken: boom (nothing was written)")
	require.Equal(t, "before", m.workspace.Self().Address, "the workspace write is undone")
	require.Equal(t, "tool", m.boundTools[0].Owner, "the tool rebind is undone")
	require.False(t, m.stateChanged)

	out, err := m.commitDangWrites([]dangCommit{writeWS, writeTool})
	require.NoError(t, err)
	require.Equal(t, "ws written (1 file) · tool state updated\ndiff --git a/x b/x", out)
	require.Equal(t, "after", m.workspace.Self().Address)
	require.Equal(t, "rebound", m.boundTools[0].Owner)
	require.True(t, m.stateChanged)
}

// `chat` is written alone, since the LLM it assigns carries its own
// workspace and tool objects, and `me` never: the Dang side refuses both
// before running a snippet, and applying refuses them again.
func TestCheckDangWrites(t *testing.T) {
	t.Parallel()
	w := func(names ...string) []DangWrite {
		var writes []DangWrite
		for _, name := range names {
			typeName := "Editor"
			switch name {
			case "chat":
				typeName = "LLM"
			case "ws":
				typeName = "Workspace"
			case "me":
				typeName = "Agent"
			}
			writes = append(writes, DangWrite{Name: name, TypeName: typeName})
		}
		return writes
	}
	require.NoError(t, checkDangWrites(w("chat")))
	require.NoError(t, checkDangWrites(w("ws", "editor")))
	err := checkDangWrites(w("ws", "chat"))
	require.ErrorContains(t, err, "write chat with `ws`: "+DangChatAloneMessage+" (nothing was written)")
	err = checkDangWrites(w("chat", "editor", "ws"))
	require.ErrorContains(t, err, "write chat with `editor`, `ws`: ")
	require.ErrorContains(t, checkDangWrites(w("me")), "`me` is read-only")

	// Nothing is loaded or written: the refusal comes first.
	m := &MCP{mu: &sync.Mutex{}}
	_, err = m.applyDangWrites(t.Context(), nil, dagql.ObjectResult[*Workspace]{}, w("ws", "chat"))
	require.ErrorContains(t, err, DangChatAloneMessage)
}

// The Dang tools' descriptions name what every scope binds for this
// conversation, and what writing each does.
func TestDangToolDescriptions(t *testing.T) {
	t.Parallel()
	full := dangBindingsDoc{ws: true, chat: true, me: true, tools: []string{"editor", "committer"}}
	check, eval := full.checkDescription(), full.evalDescription()
	require.Contains(t, check, "Every scope binds `ws` (your Workspace); `chat` (this conversation, an LLM); `me` (the Agent owning this conversation; can't be reassigned); your tool objects `editor`, `committer`.")
	require.Contains(t, eval, "a reassigned `ws` (`ws = ws.withChanges(…)`) becomes your workspace")
	require.Contains(t, eval, "a reassigned tool object (`editor = editor.someMethod(…)`) its state")
	require.Contains(t, eval, "a reassigned `chat`")
	require.Contains(t, eval, "A module installed through `ws` or `chat` is callable in the next call.")
	// Together they stay terse: every conversation pays for them.
	require.LessOrEqual(t, len(check)+len(eval), 3000, "%d + %d", len(check), len(eval))
	t.Logf("DangCheck: %d bytes, DangEval: %d bytes", len(check), len(eval))

	// Only what the conversation has: no tool objects, no agent.
	bare := dangBindingsDoc{ws: true, chat: true}
	require.Contains(t, bare.checkDescription(), "Every scope binds `ws` (your Workspace); `chat` (this conversation, an LLM).")
	for _, desc := range []string{bare.checkDescription(), bare.evalDescription()} {
		require.NotContains(t, desc, "editor")
		require.NotContains(t, desc, "`me`")
		require.NotContains(t, desc, "tool object")
	}

	// dagger mcp: no conversation, so no `chat`, nor is the name reserved.
	m := newMCP().Standalone()
	m.ownerAgent = ""
	doc := m.dangBindingsDoc()
	require.Equal(t, dangBindingsDoc{}, doc)
	require.NotContains(t, doc.checkDescription(), "Every scope binds")
	require.NotContains(t, doc.evalDescription(), "Assignment is the write")
	require.False(t, m.dangReservedNames()["chat"])
	require.True(t, newMCP().dangReservedNames()["chat"])
	// A tool object named `chat` there is written as a tool object.
	require.NoError(t, checkDangWrites([]DangWrite{{Name: "chat", TypeName: "Chat"}, {Name: "ws", TypeName: "Workspace"}}))

	owned := newMCP()
	owned.ownerAgent = "agent-1"
	require.Equal(t, dangBindingsDoc{chat: true, me: true}, owned.dangBindingsDoc())
}

// A DangEval call whose snippet reassigns `chat` is scheduled as a
// continuation, directly or wrapped in Timeout; any other isn't.
func TestDangEvalContinuationCall(t *testing.T) {
	t.Parallel()
	tools := []LLMTool{
		{Name: dangEvalToolName, Continues: func(args []byte) bool { return strings.Contains(string(args), "chat =") }},
		{Name: timeoutToolName},
	}
	m := newMCP()
	call := func(name, args string) *LLMToolCall {
		return &LLMToolCall{Name: name, Arguments: JSON(args)}
	}
	require.True(t, m.isContinuationCall(call(dangEvalToolName, `{"source":"chat = chat"}`), tools))
	require.False(t, m.isContinuationCall(call(dangEvalToolName, `{"source":"ws = ws"}`), tools))
	require.True(t, m.isContinuationCall(call(timeoutToolName, `{"tool":"DangEval","duration":"1m","arguments":{"source":"chat = chat"}}`), tools))
	require.False(t, m.isContinuationCall(call(timeoutToolName, `{"tool":"DangEval","duration":"1m","arguments":{"source":"1"}}`), tools))
}

// dangToolCallCtx is a context in a conversation's tool call, with its span,
// as a Dang tool's call runs in.
func dangToolCallCtx(t *testing.T, spanID string) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("000102030405060708090a0b0c0d0e0f")
	require.NoError(t, err)
	span, err := trace.SpanIDFromHex(spanID)
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), agentToolCallKey{}, true)
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  span,
	}))
}

// A Dang tool's report is bounded as a module tool's printed output is: the
// whole report is the tool call's logs, and a long one gives way in the
// middle to a pointer to ReadLogs on the call's span.
func TestDangToolResultBounds(t *testing.T) {
	t.Parallel()
	const spanID = "00000000000000d1"
	ctx := dangToolCallCtx(t, spanID)
	m := newMCP()

	t.Run("a report within bounds is shown whole", func(t *testing.T) {
		report := "output:\nhi\nString! = \"x\"\nok · 1ms · 0 engine calls · workspace scope\n"
		require.Equal(t, strings.TrimSuffix(report, "\n"), m.dangToolResult(ctx, DangReport{Text: report}))
	})

	t.Run("a long report points at ReadLogs", func(t *testing.T) {
		var report strings.Builder
		report.WriteString("String! = '''\n")
		for i := range 2000 {
			fmt.Fprintf(&report, "line %04d of a long file\n", i)
		}
		report.WriteString("'''\nok · 1ms · 1 engine call · workspace scope\n")
		got := m.dangToolResult(ctx, DangReport{Text: report.String()})
		require.LessOrEqual(t, len(got), llmToolLogsMaxBytes+128)
		require.Regexp(t, `\n\.\.\. \d+ lines omitted \(use ReadLogs\(span: `+spanID+`, scope: "own"\) to read more\) \.\.\.\n`, got)
		// Both ends survive: the value's head, and the footer.
		require.True(t, strings.HasPrefix(got, "String! = '''\nline 0000 of a long file\n"), got)
		require.True(t, strings.HasSuffix(got, "line 1999 of a long file\n'''\nok · 1ms · 1 engine call · workspace scope"), got)
	})

	t.Run("a long line is clamped once, on a rune boundary", func(t *testing.T) {
		got := m.dangToolResult(ctx, DangReport{Text: "String! = \"" + strings.Repeat("日", llmLogsMaxLineLen) + "\"\n"})
		require.Contains(t, got, "bytes truncated]")
		require.LessOrEqual(t, len(got), llmLogsMaxLineLen)
		require.True(t, utf8.ValidString(got))
		require.Equal(t, got, guardToolResult(got), "the result guard leaves it be")
	})

	t.Run("outside a conversation's tool call, the report is only guarded", func(t *testing.T) {
		// dagger mcp: no span of the call's own to point at.
		report := strings.Repeat("a line of the report\n", 2000)
		require.Equal(t, report, m.dangToolResult(t.Context(), DangReport{Text: report}))
		long := strings.Repeat("a line of the report\n", 5000)
		got := m.dangToolResult(trace.ContextWithSpanContext(t.Context(), trace.SpanContextFromContext(ctx)), DangReport{Text: long})
		require.Equal(t, guardToolResult(long), got)
		require.NotContains(t, got, "ReadLogs")
	})
}

// A Dang tool's source that fails to parse or type-check points at the
// dang-language skill, until the conversation reads it.
func TestDangToolResultSkillNudge(t *testing.T) {
	t.Parallel()
	ctx := dangToolCallCtx(t, "00000000000000d2")
	typeError := DangReport{Text: "<check>:1:1: error: \"nope\" not found\n1 error · workspace scope · nothing ran\n", Rejected: true}
	parseError := DangReport{Text: "<check>:1:5: error: unexpected end of input\n1 error · workspace scope · nothing ran\n", Rejected: true}
	runtimeError := DangReport{Text: "error: boom\nfailed · 1ms · 0 engine calls · workspace scope\n"}
	success := DangReport{Text: "Int! = 2\nok · 1ms · 0 engine calls · workspace scope\n"}

	m := newMCP()
	for _, rep := range []DangReport{typeError, parseError} {
		got := m.dangToolResult(ctx, rep)
		require.True(t, strings.HasSuffix(got, "nothing ran\n"+dangSkillNudge), got)
	}
	for _, rep := range []DangReport{runtimeError, success} {
		require.NotContains(t, m.dangToolResult(ctx, rep), "skill")
	}

	// Reading the skill, with ReadSkill as the model calls it, stops it.
	tools := NewLLMToolSet()
	m.loadSkillTools(&dagql.Server{}, tools)
	readSkillTool, ok := tools.Map["ReadSkill"]
	require.True(t, ok)
	_, err := readSkillTool.Call(ctx, map[string]any{"name": "nope"})
	require.Error(t, err)
	require.Contains(t, m.dangToolResult(ctx, typeError), dangSkillNudge, "a failed read reads nothing")
	_, err = readSkillTool.Call(ctx, map[string]any{"name": dangSkillName})
	require.NoError(t, err)
	require.NotContains(t, m.dangToolResult(ctx, typeError), "skill")
	require.NotContains(t, m.dangToolResult(ctx, parseError), "skill")
	// A step's reads are its own: later steps read the conversation's
	// record, which a bare clone doesn't have.
	require.Contains(t, m.Clone().dangToolResult(ctx, typeError), dangSkillNudge)
}

// A conversation has read a skill when its record holds a ReadSkill call
// naming it that succeeded.
func TestMessagesReadSkill(t *testing.T) {
	t.Parallel()
	call := func(id, tool, args string) *LLMMessage {
		return &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentToolCall, CallID: id, ToolName: tool, Arguments: JSON(args)},
		}}
	}
	result := func(id string, errored bool) *LLMMessage {
		return &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentToolResult, CallID: id, Text: "…", Errored: errored},
		}}
	}
	for _, tc := range []struct {
		name     string
		messages []*LLMMessage
		read     bool
	}{
		{"none", nil, false},
		{"read", []*LLMMessage{call("1", "ReadSkill", `{"name":"dang-language"}`), result("1", false)}, true},
		{"a reference file", []*LLMMessage{call("1", "ReadSkill", `{"name":"dang-language","file":"reference/objects.md"}`), result("1", false)}, true},
		{"another skill", []*LLMMessage{call("1", "ReadSkill", `{"name":"other"}`), result("1", false)}, false},
		{"failed", []*LLMMessage{call("1", "ReadSkill", `{"name":"dang-language"}`), result("1", true)}, false},
		{"not answered yet", []*LLMMessage{call("1", "ReadSkill", `{"name":"dang-language"}`)}, false},
		{"another tool", []*LLMMessage{call("1", "DangEval", `{"name":"dang-language"}`), result("1", false)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.read, messagesReadSkill(tc.messages, dangSkillName))
		})
	}
}

// fakeDangTools reports as core/sdk/dang/v2 does: each call logs its report
// to the tool call's span, or fails without one.
type fakeDangTools struct {
	report DangReport
	err    error
}

func (f fakeDangTools) run(ctx context.Context) (DangReport, error) {
	if f.err != nil {
		return DangReport{}, f.err
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	fmt.Fprint(stdio.Stdout, f.report.Text)
	return f.report, nil
}

func (f fakeDangTools) Check(ctx context.Context, _ DangToolEnv, _ DangCheckArgs) (DangReport, error) {
	return f.run(ctx)
}

func (f fakeDangTools) Eval(ctx context.Context, _ DangToolEnv, _ DangEvalArgs) (DangReport, error) {
	return f.run(ctx)
}

func (fakeDangTools) Reassigned(string) []string { return nil }

// A Dang tool call's span logs its report once: the tool logs it, and the
// call doesn't log its result again, but for the skill nudge it appends. A
// call that fails without a report still logs its error.
func TestDangToolCallLogsReportOnce(t *testing.T) {
	t.Parallel()
	const report = "Int! = 2\nok · 1ms · 0 engine calls · workspace scope\n"
	const rejected = "<check>:1:1: error: \"nope\" not found\n1 error · workspace scope · nothing ran\n"
	for _, tc := range []struct {
		name string
		tool string
		args string
		impl fakeDangTools
		want string
	}{
		{"check", dangCheckToolName, `{"source":"1 + 1"}`, fakeDangTools{report: DangReport{Text: report}}, report},
		{"eval", dangEvalToolName, `{"source":"1 + 1"}`, fakeDangTools{report: DangReport{Text: report}}, report},
		{"a rejected source", dangEvalToolName, `{"source":"nope"}`, fakeDangTools{report: DangReport{Text: rejected, Rejected: true}}, rejected + dangSkillNudge + "\n"},
		{"a tool error", dangEvalToolName, `{"source":"1"}`, fakeDangTools{err: errors.New("boom")}, ""},
		{"bad arguments", dangEvalToolName, `{"source":1}`, fakeDangTools{report: DangReport{Text: report}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ctx := recordingTestRecorder(t)
			recorder := &stateRecorder{}
			ctx = telemetry.WithLoggerProvider(ctx, sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder)))

			m := newMCP()
			tools := NewLLMToolSet()
			m.addDangTools(&dagql.Server{}, tools, tc.impl)
			res := m.CallContent(ctx, tools.Order, &LLMToolCall{CallID: "call_1", Name: tc.tool, Arguments: JSON(tc.args)})

			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			var logs strings.Builder
			for _, rec := range recorder.records {
				logs.WriteString(rec.body)
			}
			if tc.want == "" {
				// No report: the error result is the call's logs.
				require.True(t, res.Errored, res.ContentText())
				require.Equal(t, res.ContentText()+"\n", logs.String())
				return
			}
			require.False(t, res.Errored, res.ContentText())
			require.Equal(t, tc.want, logs.String())
		})
	}
}
