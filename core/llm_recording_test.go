package core

// The recorded-response provider must build the same per-block display spans a
// streaming provider builds. Without them every recording-backed tool call ran
// under the shared evaluation-loop context, so every recording-driven test
// exercised a telemetry shape production never has (the tool-call Boundary
// span) -- a blind spot real bugs shipped through.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func recordingTestRecorder(t *testing.T) (*tracetest.SpanRecorder, context.Context) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	// core.Tracer resolves the provider off the span in ctx.
	ctx, _ := tp.Tracer("llm-recording-test").Start(context.Background(), "root")
	return sr, ctx
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestMessageSpansCarryGenAIAgentIdentity(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	ctx = testAgentContext(t, ctx, "agent-123", "reviewer")

	emitMessageSpan(ctx, &LLMMessage{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "Please review this."}},
	}, "")
	emitMessageSpan(ctx, &LLMMessage{
		Role:    LLMMessageRoleAssistant,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "On it."}},
	}, "")

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range sr.Ended() {
		byName[span.Name()] = span
	}
	for _, name := range []string{"LLM prompt", "LLM response"} {
		span, ok := byName[name]
		require.True(t, ok, "missing %q span", name)

		id, ok := spanAttr(span, string(semconv.GenAIAgentIDKey))
		require.True(t, ok, "%s: missing GenAI agent ID", name)
		require.Equal(t, "agent-123", id.AsString())

		agentName, ok := spanAttr(span, string(semconv.GenAIAgentNameKey))
		require.True(t, ok, "%s: missing GenAI agent name", name)
		require.Equal(t, "reviewer", agentName.AsString())
	}
}

func TestDisplayToolArgsAreLineTerminatedOnce(t *testing.T) {
	for _, args := range []string{"{}", "{}\n"} {
		t.Run(fmt.Sprintf("trailing-newline-%t", strings.HasSuffix(args, "\n")), func(t *testing.T) {
			_, ctx := recordingTestRecorder(t)
			recorder := &stateRecorder{}
			provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
			ctx = telemetry.WithLoggerProvider(ctx, provider)

			dp := newDisplayPhases(ctx, "", nil)
			dp.EmitToolCall(0, "call_1", "read", args)

			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			var body strings.Builder
			for _, record := range recorder.records {
				body.WriteString(record.body)
			}
			require.Equal(t, "{}\n", body.String())
		})
	}
}

func TestMessagePreservesHeaderArgsAndTerminatesJSON(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	recorder := &stateRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
	ctx = telemetry.WithLoggerProvider(ctx, provider)
	args := `{"path":"main.go","offset":20,"limit":10,"args":["foo","bar"]}`

	emitMessageSpan(ctx, &LLMMessage{
		Role: LLMMessageRoleAssistant,
		Content: []*LLMContentBlock{{
			Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(args),
		}},
	}, "")

	ended := sr.Ended()
	require.NotEmpty(t, ended)
	span := ended[len(ended)-1]
	names, ok := spanAttr(span, telemetry.LLMToolArgNamesAttr)
	require.True(t, ok)
	values, ok := spanAttr(span, telemetry.LLMToolArgValuesAttr)
	require.True(t, ok)
	require.Equal(t, []string{"args", "limit", "offset", "path"}, names.AsStringSlice())
	require.Equal(t, []string{"foo bar", "10", "20", "main.go"}, values.AsStringSlice())

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Condition(t, func() bool {
		for _, record := range recorder.records {
			if record.body == args+"\n" {
				return true
			}
		}
		return false
	}, "message tool JSON was not newline-terminated")
}

func TestToolResultEmitsAuthoritativePatch(t *testing.T) {
	_, ctx := recordingTestRecorder(t)
	recorder := &stateRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
	ctx = telemetry.WithLoggerProvider(ctx, provider)

	patch := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	emitToolResultLogs(ctx, &LLMContentBlock{Kind: LLMContentToolResult, CallID: "call_1", Text: patch})

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, record := range recorder.records {
		if record.body == patch+"\n" {
			require.Equal(t, gitDiffContentType, record.contentType)
			return
		}
	}
	t.Fatal("authoritative patch result was not emitted")
}

func TestRecordedResponseProviderEmitsPerToolCallDisplaySpans(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	ctx = testAgentContext(t, ctx, "agent-123", "reviewer")

	provider := newRecordedResponseProvider([]*LLMMessage{
		{
			Role: LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{
				{Kind: LLMContentText, Text: "Doing the work."},
				{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{"path":"main.go"}`)},
				{Kind: LLMContentToolCall, CallID: "call_2", ToolName: "write", Arguments: JSON(`{"content":"hi"}`)},
			},
		},
	})

	res, err := provider.SendQuery(ctx, nil, nil, &LLMCallOpts{CallDigest: "sha256:deadbeef"})
	require.NoError(t, err)

	// One display span per tool call, keyed by call ID -- so CallBatch parents
	// each tool's execution beneath its own call instead of the shared ctx.
	require.Len(t, res.ToolCallDisplays, 2)
	require.Len(t, res.DisplaySpans, 3) // text + two tool calls

	seen := map[trace.SpanID]bool{}
	for _, callID := range []string{"call_1", "call_2"} {
		tc, ok := res.ToolCallDisplays[callID]
		require.True(t, ok, "no display span for %s", callID)
		sid := trace.SpanContextFromContext(tc.Ctx).SpanID()
		require.True(t, sid.IsValid())
		require.Equal(t, tc.Span.SpanContext().SpanID(), sid)
		require.False(t, seen[sid], "tool calls must not share a display span")
		seen[sid] = true
	}

	// Every display span stays open for the evaluation loop to end, exactly as
	// a streaming provider hands them back.
	require.Empty(t, sr.Ended())

	// The loop ends them exactly as it does for a live provider: CallBatch ends
	// the tool-call spans, step() ends the rest.
	endToolCallDisplay(res.ToolCallDisplays, "call_1", false, "file contents")
	endToolCallDisplay(res.ToolCallDisplays, "call_2", false, "")
	endedByCallBatch := map[trace.Span]bool{}
	for _, tc := range res.ToolCallDisplays {
		endedByCallBatch[tc.Span] = true
	}
	for _, s := range res.DisplaySpans {
		if !endedByCallBatch[s] {
			s.End()
		}
	}

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Ended() {
		byName[s.Name()] = s
	}
	for _, toolName := range []string{"read", "write"} {
		s, ok := byName[toolName]
		require.True(t, ok, "no ended span named %q", toolName)

		v, ok := spanAttr(s, telemetry.LLMToolAttr)
		require.True(t, ok, "%s: missing tool attr", toolName)
		require.Equal(t, toolName, v.AsString())

		v, ok = spanAttr(s, telemetry.UIBoundaryAttr)
		require.True(t, ok, "%s: missing boundary attr", toolName)
		require.True(t, v.AsBool())

		v, ok = spanAttr(s, telemetry.LLMRoleAttr)
		require.True(t, ok, "%s: missing role attr", toolName)
		require.Equal(t, telemetry.LLMRoleAssistant, v.AsString())

		v, ok = spanAttr(s, telemetryattrs.LLMCallDigestAttr)
		require.True(t, ok, "%s: missing call digest attr", toolName)
		require.Equal(t, "sha256:deadbeef", v.AsString())

		v, ok = spanAttr(s, string(semconv.GenAIAgentIDKey))
		require.True(t, ok, "%s: missing GenAI agent ID", toolName)
		require.Equal(t, "agent-123", v.AsString())

		v, ok = spanAttr(s, string(semconv.GenAIAgentNameKey))
		require.True(t, ok, "%s: missing GenAI agent name", toolName)
		require.Equal(t, "reviewer", v.AsString())

		// Tool calls hang off the assistant text that introduced them, the
		// same anchoring the streaming providers use.
		require.Equal(t, byName["LLM response"].SpanContext().SpanID(), s.Parent().SpanID())
	}

	// The result-size badge lands on the call that produced output.
	v, ok := spanAttr(byName["read"], telemetryattrs.LLMToolResultTokensAttr)
	require.True(t, ok, "read: missing result tokens attr")
	require.Greater(t, v.AsInt64(), int64(0))
}

// A reply's display spans start streaming before the response is recorded, so
// they open on the REQUEST state's digest. Once the withResponse call exists,
// step re-points every one of them (text and tool calls alike) at it: a branch
// from a reply must keep the reply, and the rewind marker's chain walk must
// agree.
func TestStampResponseDigestPointsDisplaySpansAtResponse(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	ctx = dagql.ContextWithCache(ctx, nil)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	requestFrame := testResultCall("withPrompt", &LLM{}, testResultCall("llm", &LLM{}, nil))
	request := llmChainResult(t, srv, requestFrame)
	responded := llmChainResult(t, srv, testResultCall("withResponse", &LLM{}, requestFrame))
	requestDigest, err := request.RecipeDigest(ctx)
	require.NoError(t, err)
	responseDigest, err := responded.RecipeDigest(ctx)
	require.NoError(t, err)
	require.NotEqual(t, requestDigest, responseDigest)

	dp := newDisplayPhases(ctx, requestDigest.String(), nil)
	dp.StartText(0)
	dp.Close(0)
	dp.EmitToolCall(1, "call_1", "read", `{"path":"main.go"}`)
	spans, _ := dp.Response()
	require.Len(t, spans, 2)

	stampResponseDigest(ctx, spans, responded)
	for _, s := range spans {
		s.End()
	}
	require.Len(t, sr.Ended(), 2)
	for _, s := range sr.Ended() {
		v, ok := spanAttr(s, telemetryattrs.LLMCallDigestAttr)
		require.True(t, ok, "%s: missing call digest attr", s.Name())
		require.Equal(t, responseDigest.String(), v.AsString(), "%s must point at its withResponse", s.Name())
	}
}

// Live telemetry exports a span only at start and end, so the tool-call span
// must know its tool's bare name and server when it starts: attributes the
// tool sets while running would leave the UI showing the raw model-facing
// name ("editor_generate") until the call finished.
func TestToolCallDisplaySpanIdentifiesToolAtStart(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)

	dp := newDisplayPhases(ctx, "", []LLMTool{
		{Name: "editor_generate", Server: "editor"},
		{Name: "CallMethod", HideSelf: true},
	})
	dp.StartToolCall(0, "call_1", "editor_generate")
	dp.StartToolCall(1, "call_2", "CallMethod")
	dp.StartToolCall(2, "call_3", "unknown_tool")

	started := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Started() {
		started[s.Name()] = s
	}
	require.Empty(t, sr.Ended(), "attributes must be checked while the spans are live")

	gen := started["editor_generate"]
	require.NotNil(t, gen)
	v, ok := spanAttr(gen, telemetry.LLMToolAttr)
	require.True(t, ok)
	require.Equal(t, "generate", v.AsString())
	v, ok = spanAttr(gen, telemetry.LLMToolServerAttr)
	require.True(t, ok)
	require.Equal(t, "editor", v.AsString())

	call := started["CallMethod"]
	require.NotNil(t, call)
	v, ok = spanAttr(call, telemetry.UIPassthroughAttr)
	require.True(t, ok)
	require.True(t, v.AsBool())

	// A tool the turn didn't offer keeps the name the model used.
	unknown := started["unknown_tool"]
	require.NotNil(t, unknown)
	v, ok = spanAttr(unknown, telemetry.LLMToolAttr)
	require.True(t, ok)
	require.Equal(t, "unknown_tool", v.AsString())
	_, ok = spanAttr(unknown, telemetry.LLMToolServerAttr)
	require.False(t, ok)
	_, ok = spanAttr(unknown, telemetryattrs.LLMToolHeaderArgNamesAttr)
	require.False(t, ok, "a tool the turn didn't offer has no schema to name its header's arguments")
}

// A live frontend only gets a tool call's header values when it ends, so the
// display span names its header's arguments when it starts, for the frontend
// to fill in from the arguments the span streams. Filled in with
// dagui.ToolHeaderArgs, they must be the values MCP.CallContent records when
// the call runs, so the header doesn't change as the span ends.
func TestToolCallDisplaySpanNamesHeaderArgsAtStart(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	tools := []LLMTool{
		{
			Name: "goCli_go", Server: "goCli",
			Schema: map[string]any{"required": []string{"args"}},
			Call:   func(context.Context, any) (any, error) { return "ok", nil },
		},
		{
			Name: "editor_write", Server: "editor",
			Schema: map[string]any{"required": []string{"filePath", "contents"}},
			Call:   func(context.Context, any) (any, error) { return "ok", nil },
		},
		{
			Name:   dangEvalToolName,
			Schema: map[string]any{"required": []string{"source"}},
			Call:   func(context.Context, any) (any, error) { return "ok", nil },
		},
	}
	for i, tc := range []struct {
		tool, args string
		names      []string
	}{
		{"goCli_go", `{"args":["vet","./engine/clientdb/",3],"env":["X=1"]}`, []string{"args", "offset", "limit"}},
		{"editor_write", `{"filePath":"a.go","contents":"package a\n","force":false}`, []string{"filePath", "contents", "offset", "limit", "args"}},
		{dangEvalToolName, `{"source":"1 + 1","at":"notes","limit":5}`, []string{"source", "offset", "limit", "args", "at", "search"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			dp := newDisplayPhases(ctx, "", tools)
			p := dp.StartToolCall(int64(i), "call", tc.tool)
			var display sdktrace.ReadOnlySpan
			for _, s := range sr.Started() {
				if s.SpanContext().SpanID() == p.span.SpanContext().SpanID() {
					display = s
				}
			}
			require.NotNil(t, display)
			v, ok := spanAttr(display, telemetryattrs.LLMToolHeaderArgNamesAttr)
			require.True(t, ok)
			require.Equal(t, tc.names, v.AsStringSlice())

			var args map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.args), &args))
			wantNames, wantValues := dagui.ToolHeaderArgs(v.AsStringSlice(), args)

			p.writeToolArgs(tc.args)
			dp.Close(int64(i))
			_, failed := newMCP().Call(p.ctx, tools, &LLMToolCall{Name: tc.tool, Arguments: JSON(tc.args)})
			require.False(t, failed)

			var ended sdktrace.ReadOnlySpan
			for _, s := range sr.Ended() {
				if s.SpanContext().SpanID() == p.span.SpanContext().SpanID() {
					ended = s
				}
			}
			require.NotNil(t, ended)
			names, ok := spanAttr(ended, telemetry.LLMToolArgNamesAttr)
			require.True(t, ok)
			values, ok := spanAttr(ended, telemetry.LLMToolArgValuesAttr)
			require.True(t, ok)
			require.Equal(t, wantNames, names.AsStringSlice())
			require.Equal(t, wantValues, values.AsStringSlice())
		})
	}
}
