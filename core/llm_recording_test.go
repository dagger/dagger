package core

// The recorded-response provider must build the same per-block display spans a
// streaming provider builds. Without them every recording-backed tool call ran
// under the shared evaluation-loop context, so every recording-driven test
// exercised a telemetry shape production never has (the tool-call Boundary
// span) -- a blind spot real bugs shipped through.

import (
	"context"
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
	}, "", nil, nil)
	emitMessageSpan(ctx, &LLMMessage{
		Role:    LLMMessageRoleAssistant,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "On it."}},
	}, "", nil, nil)

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

func TestReplayPreservesHeaderArgsAndTerminatesJSON(t *testing.T) {
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
	}, "", nil, nil)

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
	}, "replayed tool JSON was not newline-terminated")
}

func TestReplayEmitsAuthoritativePatchResult(t *testing.T) {
	_, ctx := recordingTestRecorder(t)
	recorder := &stateRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
	ctx = telemetry.WithLoggerProvider(ctx, provider)

	patch := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	llm := &LLM{Messages: []*LLMMessage{
		{
			Role: LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{{
				Kind:      LLMContentToolCall,
				CallID:    "call_1",
				ToolName:  "edit",
				Arguments: JSON(`{"filePath":"main.go"}`),
			}},
		},
		{
			Role: LLMMessageRoleUser,
			Content: []*LLMContentBlock{{
				Kind:   LLMContentToolResult,
				CallID: "call_1",
				Text:   patch,
			}},
		},
	}}
	llm.EmitHistory(ctx)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, record := range recorder.records {
		if record.body == patch+"\n" {
			require.Equal(t, gitDiffContentType, record.contentType)
			return
		}
	}
	t.Fatal("replayed patch result was not emitted")
}

func TestRecordedResponseProviderSystemPrompts(t *testing.T) {
	message := func(role LLMMessageRole, text string) *LLMMessage {
		return &LLMMessage{Role: role, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: text}}}
	}
	user := message(LLMMessageRoleUser, "task")
	user.Origin = &LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "chief", Ref: "#1"}
	system := message(LLMMessageRoleSystem, "worker instructions")
	otherSystem := message(LLMMessageRoleSystem, "other instructions")
	reply := message(LLMMessageRoleAssistant, "done")

	for _, tc := range []struct {
		name     string
		recorded []*LLMMessage
		history  []*LLMMessage
		wantErr  bool
	}{
		{
			name:     "no system prompt",
			recorded: []*LLMMessage{user, reply}, history: []*LLMMessage{user},
		},
		{
			name:     "explicit system prompt",
			recorded: []*LLMMessage{system, user, reply}, history: []*LLMMessage{system, user},
		},
		{
			name:     "multiple system prompts",
			recorded: []*LLMMessage{system, otherSystem, user, reply}, history: []*LLMMessage{system, otherSystem, user},
		},
		{
			name:     "unexpected leading system prompt is not discarded",
			recorded: []*LLMMessage{user, reply, user, reply}, history: []*LLMMessage{system, user}, wantErr: true,
		},
		{
			name:     "mismatched leading system prompt is not discarded",
			recorded: []*LLMMessage{system, user, reply}, history: []*LLMMessage{otherSystem, user}, wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := recordingTestRecorder(t)
			response, err := newRecordedResponseProvider(tc.recorded).SendQuery(ctx, renderMessagesForModel(tc.history), nil, nil)
			if tc.wantErr {
				require.ErrorContains(t, err, "message history diverges at index 0")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "done", response.TextContent())
			require.Equal(t, "task", user.TextContent(), "rendering must not alter recorded history")
		})
	}
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
}
