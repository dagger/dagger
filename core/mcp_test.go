package core

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	telemetry "github.com/dagger/otel-go"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func TestCallBatchReadOnlyOverlapsWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*Workspace](srv))
	workspace := func(address string) dagql.ObjectResult[*Workspace] {
		ws := &Workspace{Address: address}
		res, err := dagql.NewObjectResultForCall(ws, srv, &dagql.ResultCall{
			Kind: dagql.ResultCallKindSynthetic, SyntheticOp: address,
			Type: dagql.NewResultCallType(ws.Type()),
		})
		require.NoError(t, err)
		return res
	}
	initial, updated := workspace("file:///initial"), workspace("file:///updated")
	m := newMCP()
	m.workspace = initial
	// A registered server with no session exercises MCP scheduling without
	// requiring a live transport; writes take the regular-call fallback.
	m.mcpServers["external"] = &MCPServerConfig{Name: "external"}
	regularStarted, externalStarted := make(chan struct{}, 1), make(chan struct{}, 1)
	written := make(chan struct{})
	read := func(started chan<- struct{}) LLMToolFunc {
		return func(ctx context.Context, _ any) (any, error) {
			before, ok := WorkspaceFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("missing workspace before write")
			}
			started <- struct{}{}
			select {
			case <-written:
			case <-ctx.Done():
				return nil, fmt.Errorf("read did not overlap write: %w", ctx.Err())
			}
			after, ok := WorkspaceFromContext(ctx)
			if !ok {
				return nil, fmt.Errorf("missing workspace after write")
			}
			return before.Self().Address + " -> " + after.Self().Address, nil
		}
	}
	tools := []LLMTool{
		{Name: "read", ReadOnly: true, Call: read(regularStarted)},
		{Name: "external_read", Server: "external", ReadOnly: true, Call: read(externalStarted)},
		{Name: "write", Call: func(ctx context.Context, _ any) (any, error) {
			// Neither read may wait for the write lane, nor may the external
			// read wait for the regular read lane to finish.
			for _, started := range []<-chan struct{}{regularStarted, externalStarted} {
				select {
				case <-started:
				case <-ctx.Done():
					return nil, fmt.Errorf("write did not overlap both reads: %w", ctx.Err())
				}
			}
			m.workspace = updated
			return "written", nil
		}},
		{Name: "external_write", Server: "external", Call: func(context.Context, any) (any, error) {
			// Reads stay in flight through the later MCP-write phase too.
			close(written)
			return "external written", nil
		}},
		{Name: "failed_read", ReadOnly: true, Call: func(context.Context, any) (any, error) {
			return nil, fmt.Errorf("read failure")
		}},
	}
	calls := []*LLMToolCall{
		{Name: "read", CallID: "read"},
		{Name: "external_read", CallID: "external"},
		{Name: "write", CallID: "write"},
		{Name: "external_write", CallID: "external_write"},
		{Name: "failed_read", CallID: "failed"},
		{Name: "missing", CallID: "missing"},
	}
	results := batchResultsByID(t, m.CallBatch(ctx, tools, calls, nil))
	require.Len(t, results, len(calls))
	for _, id := range []string{"read", "external"} {
		require.False(t, results[id].Errored, results[id].ContentText())
		require.Equal(t, "file:///initial -> file:///initial", results[id].ContentText())
	}
	require.False(t, results["write"].Errored, results["write"].ContentText())
	require.Equal(t, "written", results["write"].ContentText())
	require.False(t, results["external_write"].Errored, results["external_write"].ContentText())
	require.Equal(t, "external written", results["external_write"].ContentText())
	require.True(t, results["failed"].Errored)
	require.Contains(t, results["failed"].ContentText(), "read failure")
	require.True(t, results["missing"].Errored)
	require.Contains(t, results["missing"].ContentText(), "missing")
	require.Same(t, updated.Self(), m.workspace.Self())

	// Reuse the tool slice: a batch snapshot must not replace the caller's
	// tool closures or leak into later batches.
	next := batchResultsByID(t, m.CallBatch(ctx, tools, calls[:2], nil))
	for _, id := range []string{"read", "external"} {
		require.False(t, next[id].Errored, next[id].ContentText())
		require.Equal(t, "file:///updated -> file:///updated", next[id].ContentText())
	}
}

func TestCallBatchWritePipelineOrdering(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m := newMCP()
	m.mcpServers["external"] = &MCPServerConfig{Name: "external"}
	var mu sync.Mutex
	var events []string
	record := func(name string, want []string) LLMToolFunc {
		return func(context.Context, any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(events, want) {
				return nil, fmt.Errorf("%s ran after %v, want %v", name, events, want)
			}
			events = append(events, name)
			// Failures must not short-circuit the remaining write pipeline.
			if name == "changeset" {
				return nil, fmt.Errorf("changeset failure")
			}
			return name, nil
		}
	}
	tools := []LLMTool{
		{Name: "first", Call: record("first", nil)},
		{Name: "second", Call: record("second", []string{"first"})},
		{Name: "changeset", ReturnsChangeset: true, Call: record("changeset", []string{"first", "second"})},
		{Name: "external_write", Server: "external", Call: record("external", []string{"first", "second", "changeset"})},
	}
	// Input order intentionally differs from phase order. Destructive regular
	// calls retain their relative order before changesets and MCP writes.
	calls := []*LLMToolCall{
		{Name: "external_write", CallID: "external"},
		{Name: "changeset", CallID: "changeset"},
		{Name: "first", CallID: "first"},
		{Name: "second", CallID: "second"},
	}
	results := batchResultsByID(t, m.CallBatch(ctx, tools, calls, nil))
	require.Equal(t, []string{"first", "second", "changeset", "external"}, events)
	require.Len(t, results, len(calls))
	for _, id := range []string{"first", "second", "external"} {
		require.False(t, results[id].Errored, results[id].ContentText())
		require.Equal(t, id, results[id].ContentText())
	}
	require.True(t, results["changeset"].Errored)
	require.Contains(t, results["changeset"].ContentText(), "changeset failure")
}

func batchResultsByID(t *testing.T, messages []*LLMMessage) map[string]*LLMContentBlock {
	t.Helper()
	results := make(map[string]*LLMContentBlock, len(messages))
	for _, message := range messages {
		require.Equal(t, LLMMessageRoleUser, message.Role)
		require.Len(t, message.Content, 1)
		result := message.Content[0]
		require.Equal(t, LLMContentToolResult, result.Kind)
		require.NotContains(t, results, result.CallID, "duplicate result")
		results[result.CallID] = result
	}
	return results
}

func TestToolErrorResponseScopesLogs(t *testing.T) {
	const traceID = "000102030405060708090a0b0c0d0e0f"
	const originID = "0000000000000001"
	const unrelatedID = "0000000000000002"
	dbs := clientdb.NewDBs(t.TempDir())
	store, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	_, err = store.AppendSpans([]clientdb.Span{
		{TraceID: traceID, SpanID: originID, Attributes: marshalSpanAttrs(t)},
		{TraceID: traceID, SpanID: unrelatedID, Attributes: marshalSpanAttrs(t)},
	})
	require.NoError(t, err)
	_, err = store.AppendLogs([]clientdb.Log{
		persistedCaptureLog(t, traceID, originID, "test", stringLogBody("source diagnostic\n")),
		persistedCaptureLog(t, traceID, originID, "test", stringLogBody("exec stdout\nexec stderr\n")),
		persistedCaptureLog(t, traceID, unrelatedID, "test", stringLogBody("unrelated output\n")),
	})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	ctx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{
		mockServer: &mockServer{}, dbs: dbs,
	}})
	failure := &ExecError{
		Err:    fmt.Errorf("failed [traceparent:%s-%s]", traceID, originID),
		Stdout: "exec stdout", Stderr: "exec stderr", ExitCode: 1,
	}
	m := newMCP()
	got := m.toolErrorResponse(ctx, failure)
	require.Contains(t, got, "source diagnostic")
	require.NotContains(t, got, "unrelated output")
	require.Contains(t, got, failure.Error())
	require.Equal(t, 1, strings.Count(got, "exec stdout"))
	require.Equal(t, 1, strings.Count(got, "exec stderr"))
	require.NotContains(t, got, "<stdout>")
	require.NotContains(t, got, "<stderr>")
	require.NotContains(t, got, "<exitCode>")
	// The payload comes entirely from telemetry, with identical output when
	// the error has no stdout/stderr extensions at all.
	require.Equal(t, got, m.toolErrorResponse(ctx, failure.Err))
	marker := fmt.Sprintf("[traceparent:%s-%s]", traceID, originID)
	fullLogs, err := m.readLogsTool(&dagql.Server{})(ctx, map[string]any{"span": marker})
	require.NoError(t, err)
	require.Equal(t, "     1→source diagnostic\n     2→exec stdout\n     3→exec stderr", fullLogs)
	tool := LLMTool{
		Name: "broken",
		Call: func(context.Context, any) (any, error) { return nil, failure },
	}
	agentResult, failed := m.Call(ctx, []LLMTool{tool}, &LLMToolCall{Name: tool.Name})
	require.True(t, failed)
	require.Equal(t, got, agentResult)
	handler := (mcpServer{env: m}).genMcpToolHandler(tool)
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool.Name}}
	request.Method = "tools/call"
	result, err := handler(ctx, request)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	require.Equal(t, got, result.Content[0].(mcp.TextContent).Text)
	// Without an origin, never fall back to the ambient session's logs.
	require.Equal(t, "plain failure", m.toolErrorResponse(ctx, fmt.Errorf("plain failure")))
	// Telemetry is best-effort: unavailable context or a failed flush/read
	// preserves the original failure, not the telemetry infrastructure error.
	require.Equal(t, failure.Error(), m.toolErrorResponse(t.Context(), failure))
	failedTelemetry := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{
		mockServer: &mockServer{}, telemetryErr: fmt.Errorf("flush failed"),
	}})
	require.Equal(t, failure.Error(), m.toolErrorResponse(failedTelemetry, failure))
}

func TestCallPreservesHeaderArgs(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	result, failed := newMCP().Call(ctx, []LLMTool{{
		Name: "read",
		Schema: map[string]any{
			"required": []string{"path"},
		},
		Call: func(context.Context, any) (any, error) { return "ok", nil },
	}}, &LLMToolCall{
		Name:      "read",
		Arguments: JSON(`{"path":"main.go","offset":20,"limit":10,"args":["foo","bar"]}`),
	})
	require.False(t, failed)
	require.Equal(t, "ok", result)
	trace.SpanFromContext(ctx).End()

	ended := sr.Ended()
	require.NotEmpty(t, ended)
	span := ended[len(ended)-1]
	names, ok := spanAttr(span, telemetry.LLMToolArgNamesAttr)
	require.True(t, ok)
	values, ok := spanAttr(span, telemetry.LLMToolArgValuesAttr)
	require.True(t, ok)
	require.Equal(t, []string{"path", "offset", "limit", "args"}, names.AsStringSlice())
	require.Equal(t, []string{"main.go", "20", "10", "foo bar"}, values.AsStringSlice())
}

func TestToolResultContentType(t *testing.T) {
	patch := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n"
	require.Equal(t, gitDiffContentType, toolResultContentType(patch))
	require.Empty(t, toolResultContentType("main.go | 1 +\n"))
	require.Empty(t, toolResultContentType("prefix\n"+patch))
}

func TestOversizedChangesetSkipsPatchWork(t *testing.T) {
	ctx := t.Context()
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass[*Changeset](srv))
	paths := &ChangesetPaths{}
	for i := range patchSummaryMaxPaths {
		paths.Added = append(paths.Added, fmt.Sprintf("new/%d", i))
		paths.AllRemoved = append(paths.AllRemoved, fmt.Sprintf("old/%d", i))
	}
	ch := &Changeset{paths: &changesetPathsMemo{paths: paths}}
	ch.paths.done.Store(true)
	ch.paths.once.Do(func() {})
	changes, err := dagql.NewObjectResultForCall(ch, srv, &dagql.ResultCall{
		Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "oversized",
		Type: dagql.NewResultCallType(ch.Type()),
	})
	require.NoError(t, err)

	// No server is needed on either oversized branch: neither may request
	// asPatch, diffStats, or an exact per-path summary.
	out := newMCP().summarizePatch(ctx, nil, changes)
	require.Contains(t, out, "exceeds the 200-path inspection budget")
	require.NotContains(t, out, "new/")
	require.NotContains(t, out, "old/")
	require.NotContains(t, out, "WARNING")
	require.Empty(t, toolResultContentType(out))
	normalized, err := normalizeChangesetToPatch(ctx, nil, changes)
	require.NoError(t, err)
	require.Same(t, ch, normalized.Self())
}

func TestSmallTextChangeset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats []*DiffStat
		want  bool
	}{
		{name: "small text", stats: []*DiffStat{{Path: "a.txt", Kind: DiffStatKindModified, AddedLines: 1, RemovedLines: 1}}, want: true},
		{name: "directory with text", stats: []*DiffStat{{Path: "new/", Kind: DiffStatKindAdded}, {Path: "new/a.txt", Kind: DiffStatKindAdded, AddedLines: 1}}, want: true},
		{name: "binary or path-only", stats: []*DiffStat{{Path: "a.bin", Kind: DiffStatKindAdded}}},
		{name: "binary with text", stats: []*DiffStat{{Path: "a.txt", Kind: DiffStatKindModified, AddedLines: 1}, {Path: "a.bin", Kind: DiffStatKindRemoved}}},
		{name: "rename", stats: []*DiffStat{{Path: "new.txt", Kind: DiffStatKindRenamed}}},
		{name: "rename with edits", stats: []*DiffStat{{Path: "new.txt", Kind: DiffStatKindRenamed, AddedLines: 1, RemovedLines: 1}}},
		{name: "at line budget", stats: []*DiffStat{{Path: "a.txt", Kind: DiffStatKindAdded, AddedLines: patchSummaryMaxLines}}, want: true},
		{name: "over line budget across files", stats: []*DiffStat{{Path: "a.txt", Kind: DiffStatKindAdded, AddedLines: patchSummaryMaxLines}, {Path: "b.txt", Kind: DiffStatKindRemoved, RemovedLines: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, smallTextChangeset(tc.stats))
		})
	}
}

func TestReadPatchPreview(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch string
		want  bool
	}{
		{name: "small text", patch: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n", want: true},
		{name: "empty"},
		{name: "at byte budget", patch: strings.Repeat("x", patchSummaryMaxBytes), want: true},
		{name: "over byte budget", patch: strings.Repeat("x", patchSummaryMaxBytes+1)},
		{name: "at line budget", patch: strings.Repeat("+x\n", patchSummaryMaxLines), want: true},
		{name: "over line budget", patch: strings.Repeat("+x\n", patchSummaryMaxLines+1)},
		{name: "binary", patch: "diff --git a/a b/a\nGIT binary patch\nliteral 3\nabc\n"},
		{name: "text mentioning binary marker", patch: "diff --git a/a b/a\n+GIT binary patch\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preview, ok := readPatchPreview(strings.NewReader(tc.patch))
			require.Equal(t, tc.want, ok)
			if ok {
				require.Equal(t, tc.patch, preview)
			} else {
				require.Empty(t, preview)
			}
		})
	}

	t.Run("read error", func(t *testing.T) {
		preview, ok := readPatchPreview(iotest.ErrReader(io.ErrUnexpectedEOF))
		require.False(t, ok)
		require.Empty(t, preview)
	})
	t.Run("read is bounded", func(t *testing.T) {
		r := strings.NewReader(strings.Repeat("x", patchSummaryMaxBytes*2))
		preview, ok := readPatchPreview(r)
		require.False(t, ok)
		require.Empty(t, preview)
		require.Equal(t, patchSummaryMaxBytes-1, r.Len())
	})
}

func TestCallMarksPatchResult(t *testing.T) {
	recorder, ctx := stateRecorderCtx(t)
	patch := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n"
	result, failed := newMCP().Call(ctx, []LLMTool{{
		Name: "edit",
		Call: func(context.Context, any) (any, error) {
			return patch, nil
		},
	}}, &LLMToolCall{Name: "edit"})
	require.False(t, failed)
	require.Equal(t, patch, result)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, record := range recorder.records {
		if record.body == patch+"\n" {
			require.Equal(t, gitDiffContentType, record.contentType)
			return
		}
	}
	t.Fatal("tool-result log was not emitted")
}

func TestTimeoutTool(t *testing.T) {
	timeoutArgs := func(duration, tool string, arguments map[string]any) map[string]any {
		return map[string]any{
			"duration":  duration,
			"tool":      tool,
			"arguments": arguments,
		}
	}

	t.Run("schema", func(t *testing.T) {
		tool := timeoutToolForTest(t)
		require.False(t, tool.ReadOnly)
		require.Equal(t, []string{"duration", "tool", "arguments"}, tool.Schema["required"])
		properties := tool.Schema["properties"].(map[string]any)
		require.Equal(t, "string", properties["duration"].(map[string]any)["type"])
		require.Equal(t, "string", properties["tool"].(map[string]any)["type"])
		require.Equal(t, "object", properties["arguments"].(map[string]any)["type"])
	})

	t.Run("success", func(t *testing.T) {
		wantArgs := map[string]any{"message": "hello"}
		wantResult := map[string]any{"status": "ok"}
		var gotArgs any
		tool := timeoutToolForTest(t, LLMTool{
			Name: "echo",
			Call: func(_ context.Context, args any) (any, error) {
				gotArgs = args
				return wantResult, nil
			},
		})

		result, err := tool.Call(t.Context(), timeoutArgs("1s", "echo", wantArgs))
		require.NoError(t, err)
		// The nested call goes through MCP.Call, which renders a non-string
		// result the way the model would see it.
		require.JSONEq(t, `{"status":"ok"}`, result.(string))
		require.Equal(t, wantArgs, gotArgs)
	})

	t.Run("deadline cancellation reaches nested tool", func(t *testing.T) {
		observed := make(chan error, 1)
		tool := timeoutToolForTest(t, LLMTool{
			Name: "wait",
			Call: func(ctx context.Context, _ any) (any, error) {
				select {
				case <-ctx.Done():
					observed <- ctx.Err()
					return nil, ctx.Err()
				case <-time.After(time.Second):
					return nil, fmt.Errorf("nested context was not cancelled")
				}
			},
		})

		_, err := tool.Call(t.Context(), timeoutArgs("10ms", "wait", map[string]any{}))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.ErrorContains(t, err, `tool "wait" did not finish within 10ms`)
		require.ErrorIs(t, <-observed, context.DeadlineExceeded)
	})

	t.Run("invalid duration", func(t *testing.T) {
		called := false
		tool := timeoutToolForTest(t, LLMTool{
			Name: "echo",
			Call: func(context.Context, any) (any, error) {
				called = true
				return nil, nil
			},
		})

		_, err := tool.Call(t.Context(), timeoutArgs("eventually", "echo", map[string]any{}))
		require.ErrorContains(t, err, `invalid timeout duration "eventually"`)
		require.False(t, called)
	})

	t.Run("unavailable target", func(t *testing.T) {
		tool := timeoutToolForTest(t)
		_, err := tool.Call(t.Context(), timeoutArgs("1s", "hidden", map[string]any{}))
		require.EqualError(t, err, `tool "hidden" is not available`)
	})

	t.Run("self wrapping", func(t *testing.T) {
		tool := timeoutToolForTest(t, LLMTool{
			Name: "echo",
			Call: func(_ context.Context, args any) (any, error) {
				return args, nil
			},
		})
		innerArgs := map[string]any{"message": "hello"}

		result, err := tool.Call(t.Context(), timeoutArgs("1s", "Timeout",
			timeoutArgs("1s", "echo", innerArgs)))
		require.NoError(t, err)
		require.JSONEq(t, `{"message":"hello"}`, result.(string))
	})

	t.Run("nested call is traced as a tool call of its own", func(t *testing.T) {
		sr, ctx := recordingTestRecorder(t)
		tool := timeoutToolForTest(t, LLMTool{
			Name:   "echo",
			Server: "Echoes",
			Call: func(ctx context.Context, _ any) (any, error) {
				// Dispatched through MCP.Call, the nested tool runs beneath a
				// tool-call span of its own rather than the Timeout span.
				require.True(t, trace.SpanFromContext(ctx).SpanContext().IsValid())
				return "echoed", nil
			},
		})
		_, err := tool.Call(ctx, timeoutArgs("1s", "echo", map[string]any{"message": "hello"}))
		require.NoError(t, err)

		var found bool
		for _, span := range sr.Ended() {
			if span.Name() != "echo" {
				continue
			}
			found = true
			toolAttr, ok := spanAttr(span, telemetry.LLMToolAttr)
			require.True(t, ok)
			require.Equal(t, "echo", toolAttr.AsString())
			serverAttr, ok := spanAttr(span, telemetry.LLMToolServerAttr)
			require.True(t, ok)
			require.Equal(t, "Echoes", serverAttr.AsString())
		}
		require.True(t, found, "no tool-call span for the nested call")
	})
}

func timeoutToolForTest(t *testing.T, targets ...LLMTool) *LLMTool {
	t.Helper()
	m := newMCP()
	allTools := NewLLMToolSet()
	for _, target := range targets {
		require.True(t, allTools.Add(target))
	}
	m.loadBuiltins(nil, allTools)
	tool, err := m.LookupTool("Timeout", allTools.Order)
	require.NoError(t, err)
	return tool
}

func TestGenMCPToolPreservesSchema(t *testing.T) {
	tool, err := genMcpTool(LLMTool{
		Name:        "commit",
		Description: "Commit changes.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"date": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "string"},
						map[string]any{"type": "null"},
					},
					"default": nil,
				},
			},
			"additionalProperties": false,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "commit", tool.Name)
	require.Equal(t, "Commit changes.", tool.Description)
	require.JSONEq(t, `{
		"type":"object",
		"properties":{
			"date":{
				"anyOf":[{"type":"string"},{"type":"null"}],
				"default":null
			}
		},
		"additionalProperties":false
	}`, string(tool.RawInputSchema))
}

// TestAssembleLines covers log-line assembly from raw stdio segments: log
// records aren't guaranteed to be line-aligned, so a line that straddles two
// records takes the provenance of the record that started it.
func TestAssembleLines(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments []capturedLine
		want     []capturedLine
	}{
		{
			name: "whole lines keep their provenance",
			segments: []capturedLine{
				{text: "nested one\nnested two\n", direct: false},
				{text: "printed\n", direct: true},
			},
			want: []capturedLine{
				{text: "nested one", direct: false},
				{text: "nested two", direct: false},
				{text: "printed", direct: true},
			},
		},
		{
			name: "line split across records is assembled once",
			segments: []capturedLine{
				{text: "hello, ", direct: true},
				{text: "world\n", direct: true},
			},
			want: []capturedLine{{text: "hello, world", direct: true}},
		},
		{
			name: "straddling line takes the starting record's provenance",
			segments: []capturedLine{
				{text: "start", direct: true},
				{text: "-end\nnested\n", direct: false},
			},
			want: []capturedLine{
				{text: "start-end", direct: true},
				{text: "nested", direct: false},
			},
		},
		{
			name:     "trailing newlines don't contribute lines",
			segments: []capturedLine{{text: "only\n\n\n", direct: true}},
			want:     []capturedLine{{text: "only", direct: true}},
		},
		{
			name:     "unterminated final line is kept",
			segments: []capturedLine{{text: "no trailing newline", direct: false}},
			want:     []capturedLine{{text: "no trailing newline", direct: false}},
		},
		{
			name:     "empty input",
			segments: nil,
			want:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assembleLines(tc.segments)
			if len(got) != len(tc.want) {
				t.Fatalf("assembleLines() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

type logCaptureTestServer struct {
	*mockServer
	dbs          *clientdb.DBs
	telemetryErr error
}

func (srv *logCaptureTestServer) ClientTelemetry(ctx context.Context, _, _ string) (*clientdb.DB, error) {
	if srv.telemetryErr != nil {
		return nil, srv.telemetryErr
	}
	return srv.dbs.Open(ctx, "capture-test")
}

// TestCallPayloadRecordsExcludedFromLLMLogs covers both LLM-facing consumers
// of captureLogLines: the explicit ReadLogs builtin and automatic tool-result
// log capture. A marker key reserves its record regardless of value or type;
// the dedicated scope independently reserves malformed records with no marker.
func TestCallPayloadRecordsExcludedFromLLMLogs(t *testing.T) {
	const (
		traceID = "000102030405060708090a0b0c0d0e0f"
		spanID  = "0000000000000001"
	)

	dbs := clientdb.NewDBs(t.TempDir())
	store, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	_, err = store.AppendSpans([]clientdb.Span{{
		TraceID:    traceID,
		SpanID:     spanID,
		Attributes: marshalSpanAttrs(t),
	}})
	require.NoError(t, err)

	ordinaryScope := "ordinary.logs"
	callContentType := &otlpcommonv1.KeyValue{
		Key: telemetry.ContentTypeAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{
			StringValue: telemetryattrs.CallPayloadContentType,
		}},
	}
	logs := []clientdb.Log{
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, stringLogBody("before\n")),
		// A well-formed payload record: reserved by content type.
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, bytesLogBody([]byte("CALL-PAYLOAD-BYTES")), callContentType),
		// Malformed: the payload content type over a text body is still
		// reserved, never rendered.
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, stringLogBody("CONTENT-TYPE-WRONG-BODY\n"), callContentType),
		// No content type at all: not a call payload, but binary data all the
		// same. Only string bodies may become LLM-visible text.
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, bytesLogBody([]byte("UNTYPED-BYTES"))),
		persistedCaptureLog(t, traceID, spanID, ordinaryScope, stringLogBody("after\n")),
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	ctx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{
		mockServer: &mockServer{},
		dbs:        dbs,
	}})
	traceIDValue, err := trace.TraceIDFromHex(traceID)
	require.NoError(t, err)
	spanIDValue, err := trace.SpanIDFromHex(spanID)
	require.NoError(t, err)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceIDValue,
		SpanID:  spanIDValue,
	}))
	m := newMCP()

	t.Run("ReadLogs", func(t *testing.T) {
		got, err := m.readLogsTool(&dagql.Server{})(ctx, map[string]any{"span": spanID})
		require.NoError(t, err)
		require.Equal(t, "     1→before\n     2→after", got)
	})

	t.Run("automatic tool result", func(t *testing.T) {
		require.Equal(t, "before\nafter", m.toolLogs(ctx))
	})
}

func persistedCaptureLog(t *testing.T, traceID, spanID, scope string, body *otlpcommonv1.AnyValue, attrs ...*otlpcommonv1.KeyValue) clientdb.Log {
	t.Helper()
	bodyBytes, err := proto.Marshal(body)
	require.NoError(t, err)
	attrBytes, err := clientdb.MarshalProtoJSONs(attrs)
	require.NoError(t, err)
	scopeBytes, err := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: scope})
	require.NoError(t, err)
	return clientdb.Log{
		TraceID:              sql.NullString{String: traceID, Valid: true},
		SpanID:               sql.NullString{String: spanID, Valid: true},
		Body:                 bodyBytes,
		Attributes:           attrBytes,
		InstrumentationScope: scopeBytes,
	}
}

func stringLogBody(value string) *otlpcommonv1.AnyValue {
	return &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}
}

func bytesLogBody(value []byte) *otlpcommonv1.AnyValue {
	return &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: value}}
}

// TestLimitIndirectLines locks in the tool-result abridging rule: whatever the
// tool printed itself survives in full — a sub-agent's report is the point of
// the call — while logs from nested work beneath it are cut down to a tail,
// with the dropped runs counted so the model knows to reach for ReadLogs.
func TestLimitIndirectLines(t *testing.T) {
	direct := func(texts ...string) []capturedLine {
		lines := make([]capturedLine, len(texts))
		for i, text := range texts {
			lines[i] = capturedLine{text: text, direct: true}
		}
		return lines
	}
	nested := func(texts ...string) []capturedLine {
		lines := make([]capturedLine, len(texts))
		for i, text := range texts {
			lines[i] = capturedLine{text: text}
		}
		return lines
	}

	t.Run("a long report survives noisy nested work", func(t *testing.T) {
		// The shape that motivated this: a sub-agent runs a build (lots of
		// nested output), then the tool prints its report. Tail-only
		// truncation evicted the report; now only the build output is cut.
		var lines []capturedLine
		for i := 1; i <= 50; i++ {
			lines = append(lines, capturedLine{text: fmt.Sprintf("NESTED-%02d", i)})
		}
		for i := 1; i <= 12; i++ {
			lines = append(lines, capturedLine{text: fmt.Sprintf("REPORT-%02d", i), direct: true})
		}

		got := limitIndirectLines("abc123", lines, 3, 1000)
		want := []string{
			"... 47 lines omitted (use ReadLogs(span: abc123) to read more) ...",
			"NESTED-48", "NESTED-49", "NESTED-50",
		}
		for i := 1; i <= 12; i++ {
			want = append(want, fmt.Sprintf("REPORT-%02d", i))
		}
		requireLines(t, got, want)
	})

	t.Run("direct lines are never dropped even past the limit", func(t *testing.T) {
		got := limitIndirectLines("s", direct("a", "b", "c", "d", "e"), 2, 1000)
		requireLines(t, got, []string{"a", "b", "c", "d", "e"})
	})

	t.Run("interleaved runs each report their own count", func(t *testing.T) {
		var lines []capturedLine
		lines = append(lines, nested("n1", "n2", "n3")...)
		lines = append(lines, direct("report A")...)
		lines = append(lines, nested("n4", "n5")...)
		lines = append(lines, direct("report B")...)

		// limit 1 keeps only the last indirect line (n5).
		got := limitIndirectLines("s", lines, 1, 1000)
		requireLines(t, got, []string{
			"... 3 lines omitted (use ReadLogs(span: s) to read more) ...",
			"report A",
			"... 1 lines omitted (use ReadLogs(span: s) to read more) ...",
			"n5",
			"report B",
		})
	})

	t.Run("nothing is abridged when nested output fits", func(t *testing.T) {
		lines := append(nested("n1", "n2"), direct("done")...)
		got := limitIndirectLines("s", lines, 8, 1000)
		requireLines(t, got, []string{"n1", "n2", "done"})
	})

	t.Run("zero limit keeps everything", func(t *testing.T) {
		lines := append(nested("n1", "n2", "n3"), direct("done")...)
		got := limitIndirectLines("s", lines, 0, 1000)
		requireLines(t, got, []string{"n1", "n2", "n3", "done"})
	})

	t.Run("long lines are still character-capped", func(t *testing.T) {
		long := strings.Repeat("x", 20)
		got := limitIndirectLines("s", direct(long), 8, 10)
		requireLines(t, got, []string{strings.Repeat("x", 10) + "[... 10 chars truncated]"})
	})

	t.Run("a runaway direct print hits the byte cap", func(t *testing.T) {
		// Direct lines are never dropped by the line limit, so the byte cap is
		// the only bound on a tool that prints far more than any report: the
		// head and tail survive around a counted marker, and the total stays
		// within budget.
		var lines []capturedLine
		for i := 1; i <= 40; i++ {
			lines = append(lines, capturedLine{text: fmt.Sprintf("LINE-%02d-%s", i, strings.Repeat("x", 995)), direct: true})
		}
		got := limitIndirectLines("s", lines, 8, 2000)
		if len(got) >= 40 {
			t.Fatalf("got %d lines, want the byte cap to drop some", len(got))
		}
		joined := strings.Join(got, "\n")
		if len(joined) > llmToolLogsMaxBytes+100 {
			t.Errorf("joined output is %d bytes, want within ~%d", len(joined), llmToolLogsMaxBytes)
		}
		if !strings.HasPrefix(got[0], "LINE-01") {
			t.Errorf("first line = %.20q, want the head to survive", got[0])
		}
		if !strings.HasPrefix(got[len(got)-1], "LINE-40") {
			t.Errorf("last line = %.20q, want the tail to survive", got[len(got)-1])
		}
		if !strings.Contains(joined, "lines omitted (use ReadLogs(span: s)") {
			t.Errorf("output lacks the counted ReadLogs marker:\n%s", joined)
		}
	})
}

// TestCapLinesBytes covers the last-resort byte cap on captured tool logs:
// under budget the lines pass through untouched; over budget the middle is
// dropped behind a counted marker, with the head keeping the larger share
// and at least one line surviving on each side.
func TestCapLinesBytes(t *testing.T) {
	t.Run("under budget passes through", func(t *testing.T) {
		lines := []string{"one", "two", "three"}
		requireLines(t, capLinesBytes("s", lines, 1000), lines)
	})

	t.Run("zero budget disables the cap", func(t *testing.T) {
		lines := []string{strings.Repeat("x", 100)}
		requireLines(t, capLinesBytes("s", lines, 0), lines)
	})

	t.Run("over budget drops the middle behind a marker", func(t *testing.T) {
		// 10 lines of 10 bytes (11 with newline); budget 66 → head budget 44
		// keeps 4 lines, tail budget 22 keeps 2, marker counts the 4 dropped.
		var lines []string
		for i := 1; i <= 10; i++ {
			lines = append(lines, fmt.Sprintf("line-%02dxxx", i))
		}
		got := capLinesBytes("s", lines, 66)
		requireLines(t, got, []string{
			"line-01xxx", "line-02xxx", "line-03xxx", "line-04xxx",
			"... 4 lines omitted (use ReadLogs(span: s) to read more) ...",
			"line-09xxx", "line-10xxx",
		})
	})

	t.Run("an oversized boundary line still survives", func(t *testing.T) {
		// The first and last lines are always kept even when either alone
		// exceeds its share of the budget.
		lines := []string{strings.Repeat("a", 50), "mid", strings.Repeat("z", 50)}
		got := capLinesBytes("s", lines, 40)
		requireLines(t, got, []string{
			strings.Repeat("a", 50),
			"... 1 lines omitted (use ReadLogs(span: s) to read more) ...",
			strings.Repeat("z", 50),
		})
	})
}

func requireLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines:\n%s\nwant %d lines:\n%s",
			len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestGuardToolResult covers the last-resort bound on a tool's return value.
// The motivating case: editor_read on a 6-line JSON file whose second line is
// a ~400 KB base64 blob (a saved session's llm_id). The call's own `limit: 15`
// counted LINES, so the module's limit never fired and the blob landed
// verbatim in the model's context.
// TestGuardToolResult: a result within both limits is byte-identical.
func TestGuardToolResultInBudgetIsIdentical(t *testing.T) {
	for _, res := range []string{
		"",
		"(done)",
		"{\"ok\":true}",
		strings.Repeat("a line of ordinary tool output\n", 500),
		strings.Repeat("x", llmLogsMaxLineLen), // exactly at the line clamp
	} {
		if got := guardToolResult(res); got != res {
			t.Fatalf("guardToolResult modified an in-budget result of %d bytes:\ngot  %.80q\nwant %.80q",
				len(res), got, res)
		}
	}
}

// TestGuardToolResult: a single monster line is clamped, the rest survives.
func TestGuardToolResultClampsMonsterLine(t *testing.T) {
	blob := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", 12000) // ~420 KB
	res := "{\n" +
		"  \"llm_id\": \"" + blob + "\",\n" +
		"  \"model\": \"claude\",\n" +
		"  \"messages\": 3,\n" +
		"  \"tokens\": 1234\n" +
		"}"

	got := guardToolResult(res)
	if len(got) > llmToolResultMaxBytes {
		t.Fatalf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected the 6 lines to survive as lines, got %d", len(lines))
	}
	for i, want := range map[int]string{
		0: "{",
		2: "  \"model\": \"claude\",",
		3: "  \"messages\": 3,",
		4: "  \"tokens\": 1234",
		5: "}",
	} {
		if lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if !strings.HasPrefix(lines[1], "  \"llm_id\": \""+blob[:100]) {
		t.Errorf("clamped line lost its head: %.60q", lines[1])
	}
	if !strings.HasSuffix(lines[1], " bytes truncated]") {
		t.Errorf("clamped line lacks its inline marker: %.60q", lines[1][max(0, len(lines[1])-60):])
	}
	if n := len(lines[1]); n > llmLogsMaxLineLen+64 {
		t.Errorf("clamped line is %d bytes, want ~%d", n, llmLogsMaxLineLen)
	}
	// The whole point: the blob does not reach the model.
	if len(got) > 4*1024 {
		t.Errorf("guarded result is %d bytes (from %d), want the blob gone", len(got), len(res))
	}
}

// TestGuardToolResult: a total-budget blowout drops the middle.
func TestGuardToolResultDropsMiddleOverBudget(t *testing.T) {
	var lines []string
	lines = append(lines, "FIRST LINE OF THE RESULT")
	for i := range 20000 {
		lines = append(lines, fmt.Sprintf("%6d: some ordinary line of file content", i))
	}
	lines = append(lines, "LAST LINE OF THE RESULT")
	res := strings.Join(lines, "\n")

	got := guardToolResult(res)
	if len(got) > llmToolResultMaxBytes {
		t.Fatalf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
	if !strings.HasPrefix(got, "FIRST LINE OF THE RESULT\n") {
		t.Errorf("head did not survive: %.60q", got)
	}
	if !strings.HasSuffix(got, "\nLAST LINE OF THE RESULT") {
		t.Errorf("tail did not survive: %.60q", got[max(0, len(got)-60):])
	}

	// Exactly one marker, and every other line is a whole input line.
	input := map[string]bool{}
	for _, line := range lines {
		input[line] = true
	}
	var markers int
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "omitted from the middle of this result") {
			markers++
			if !strings.Contains(line, "re-run the call more narrowly") {
				t.Errorf("marker doesn't say what to do about it: %q", line)
			}
			continue
		}
		if !input[line] {
			t.Errorf("kept line is not a whole input line: %q", line)
		}
	}
	if markers != 1 {
		t.Fatalf("expected exactly one marker, got %d", markers)
	}

	// Both ends are generous: the head keeps its two-thirds share, and the
	// tail is not a token gesture.
	head, tail, ok := strings.Cut(got, "... ")
	if !ok {
		t.Fatal("expected to find the marker")
	}
	if len(head) < llmToolResultMaxBytes/2 {
		t.Errorf("head is only %d bytes of a %d-byte budget", len(head), llmToolResultMaxBytes)
	}
	if len(tail) < 1024 {
		t.Errorf("tail is only %d bytes", len(tail))
	}
}

// TestGuardToolResult: multi-byte runes are never split.
func TestGuardToolResultNeverSplitsRunes(t *testing.T) {
	// A tree-drawing report: 3-byte runes straddling both the per-line
	// clamp and the total budget.
	res := strings.Repeat("┃", 5000) + "\n" +
		strings.Repeat(strings.Repeat("◼ ┃ a nested span row\n", 1), 5000)

	got := guardToolResult(res)
	if !utf8.ValidString(got) {
		t.Fatal("guard split a rune: result is not valid UTF-8")
	}
	if len(got) > llmToolResultMaxBytes {
		t.Fatalf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
}

// TestGuardToolResult: degenerate sizes.
func TestGuardToolResultDegenerateSizes(t *testing.T) {
	// One line, longer than the whole budget: the per-line clamp alone
	// brings it back inside.
	single := strings.Repeat("y", llmToolResultMaxBytes+1)
	got := guardToolResult(single)
	if len(got) > llmToolResultMaxBytes {
		t.Errorf("single-line result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
	if !strings.HasPrefix(got, strings.Repeat("y", llmLogsMaxLineLen)) {
		t.Errorf("single-line result lost its head: %.40q", got)
	}

	// A result exactly at the budget passes through; one line more does
	// not, and still comes back within budget.
	line := strings.Repeat("z", 63) // 64 bytes with its newline
	atBudget := strings.TrimSuffix(strings.Repeat(line+"\n", llmToolResultMaxBytes/64), "\n")
	if len(atBudget) != llmToolResultMaxBytes-1 {
		t.Fatalf("test setup: %d bytes, want %d", len(atBudget), llmToolResultMaxBytes-1)
	}
	if got := guardToolResult(atBudget); got != atBudget {
		t.Errorf("a result at the budget was modified (%d -> %d bytes)", len(atBudget), len(got))
	}
	overBudget := atBudget + "\n" + line
	got = guardToolResult(overBudget)
	if got == overBudget {
		t.Error("a result over the budget passed through untouched")
	}
	if len(got) > llmToolResultMaxBytes {
		t.Errorf("guarded result is %d bytes, over the %d-byte budget", len(got), llmToolResultMaxBytes)
	}
}

// TestInternalSpanFilterSubtreeBounds covers beneathInternal's containment
// rule: a cause-linked span's parent chain leaves the captured subtree
// without passing through the capture root (a service exec span is parented
// under whatever call triggered the start), so internal spans on that
// unrelated chain must not hide the capture's logs. Internal-ness within the
// subtree still hides, service filtering still applies to the cause-linked
// exec span itself, and refresh picks up spans that joined the subtree after
// the filter's snapshot.
func TestInternalSpanFilterSubtreeBounds(t *testing.T) {
	ctx := context.Background()
	store, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "filter-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Link targets round-trip through OTLP span IDs, so use valid hex IDs.
	const (
		traceID     = "000102030405060708090a0b0c0d0e0f"
		rootSpan    = "0000000000000001" // trace root
		hiddenSpan  = "0000000000000002" // internal span OUTSIDE the capture
		triggerSpan = "0000000000000003" // triggered the service start, beneath hiddenSpan
		installSpan = "0000000000000004" // Container.asService: the capture root
		execSpan    = "0000000000000005" // service exec span, cause-links to installSpan
		execChild   = "0000000000000006" // e.g. the exec's process span
		innerHidden = "0000000000000007" // internal span WITHIN the capture
		innerLeaf   = "0000000000000008" // beneath innerHidden; appended late
	)

	internalAttrs := marshalSpanAttrs(t, boolAttr(telemetry.UIInternalAttr))
	serviceAttrs := marshalSpanAttrs(t, boolAttr(telemetryattrs.ServiceAttr))
	noAttrs := marshalSpanAttrs(t)

	_, err = store.AppendSpans([]clientdb.Span{
		{TraceID: traceID, SpanID: rootSpan, Attributes: noAttrs},
		{TraceID: traceID, SpanID: hiddenSpan, ParentSpanID: validSpanID(rootSpan), Attributes: internalAttrs},
		{TraceID: traceID, SpanID: triggerSpan, ParentSpanID: validSpanID(hiddenSpan), Attributes: noAttrs},
		{TraceID: traceID, SpanID: installSpan, ParentSpanID: validSpanID(rootSpan), Attributes: noAttrs},
		{TraceID: traceID, SpanID: execSpan, ParentSpanID: validSpanID(triggerSpan),
			Attributes: serviceAttrs, Links: marshalCauseLink(t, traceID, installSpan)},
		{TraceID: traceID, SpanID: execChild, ParentSpanID: validSpanID(execSpan), Attributes: noAttrs},
		{TraceID: traceID, SpanID: innerHidden, ParentSpanID: validSpanID(installSpan), Attributes: internalAttrs},
	})
	if err != nil {
		t.Fatal(err)
	}

	f := newInternalSpanFilter(store, installSpan, false)

	// The exec child's chain (execSpan → triggerSpan → hiddenSpan) leaves the
	// subtree at triggerSpan; the internal span above is not between these
	// logs and the capture root and must not hide them.
	if beneathInternalOrFatal(t, f, traceID, execChild) {
		t.Error("execChild hidden by an internal span outside the captured subtree")
	}

	// innerLeaf hasn't been appended: it's outside the snapshot, unhidden.
	if beneathInternalOrFatal(t, f, traceID, innerLeaf) {
		t.Error("innerLeaf hidden before joining the subtree")
	}
	_, err = store.AppendSpans([]clientdb.Span{
		{TraceID: traceID, SpanID: innerLeaf, ParentSpanID: validSpanID(innerHidden), Attributes: noAttrs},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.refresh()
	// Now within the subtree, beneath an internal span: hidden.
	if !beneathInternalOrFatal(t, f, traceID, innerLeaf) {
		t.Error("innerLeaf not hidden by an internal ancestor within the subtree")
	}

	// Service filtering still applies to the cause-linked exec span itself:
	// it IS within the capture — that's how its logs got here.
	sf := newInternalSpanFilter(store, installSpan, true)
	if !beneathInternalOrFatal(t, sf, traceID, execChild) {
		t.Error("execChild not filtered as service logs with skipServices set")
	}
}

func beneathInternalOrFatal(t *testing.T, f *internalSpanFilter, traceID, spanID string) bool {
	t.Helper()
	hidden, err := f.beneathInternal(context.Background(), traceID, spanID)
	if err != nil {
		t.Fatal(err)
	}
	return hidden
}

func boolAttr(key string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{
		Key:   key,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}},
	}
}

func marshalSpanAttrs(t *testing.T, attrs ...*otlpcommonv1.KeyValue) []byte {
	t.Helper()
	data, err := clientdb.MarshalProtoJSONs(attrs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func marshalCauseLink(t *testing.T, traceID, spanID string) []byte {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := clientdb.MarshalProtoJSONs(telemetry.SpanLinksToPB([]sdktrace.Link{{
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}),
		Attributes:  []attribute.KeyValue{attribute.String(telemetry.LinkPurposeAttr, telemetry.LinkPurposeCause)},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validSpanID(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// TestNormalizeSpanArg covers span-argument normalization: agents paste span
// IDs in whatever form they last saw them — bare hex, the "span=<hex>" report
// rendering, or a traceparent marker from an error — and all of them should
// resolve to the bare span ID.
func TestNormalizeSpanArg(t *testing.T) {
	const (
		traceID = "000102030405060708090a0b0c0d0e0f"
		spanID  = "00000000000000cc"
	)
	for _, tc := range []struct {
		name string
		arg  string
		want string
	}{
		{name: "bare span ID", arg: spanID, want: spanID},
		{name: "surrounding whitespace", arg: "  " + spanID + "\n", want: spanID},
		{name: "span= report rendering", arg: "span=" + spanID, want: spanID},
		{name: "error-origin marker", arg: "[traceparent:" + traceID + "-" + spanID + "]", want: spanID},
		{name: "traceparent prefix", arg: "traceparent:" + traceID + "-" + spanID, want: spanID},
		{name: "w3c traceparent", arg: "00-" + traceID + "-" + spanID + "-01", want: spanID},
		{name: "unrecognized input passes through", arg: "not-a-span", want: "not-a-span"},
		{name: "16 chars but not hex passes through", arg: "zzzzzzzzzzzzzzzz", want: "zzzzzzzzzzzzzzzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSpanArg(tc.arg); got != tc.want {
				t.Errorf("normalizeSpanArg(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

// TestRenderReadLogs covers ReadLogs result shaping: offset/grep/limit
// handling, and — for agent recovery — that the failure and empty cases
// report how many lines actually exist.
func TestRenderReadLogs(t *testing.T) {
	logLines := func() []string { return []string{"alpha", "beta", "gamma"} }

	t.Run("numbers the lines", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "     1→alpha\n     2→beta\n     3→gamma"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("offset trims from the end", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 1, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "     1→alpha\n     2→beta"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("negative offset reads the tail", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), -5, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "gamma") {
			t.Errorf("got %q, want the full tail", got)
		}
	})

	t.Run("offset past the start reports the total", func(t *testing.T) {
		_, err := renderReadLogs("s", logLines(), 3, 100, "")
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "3 available lines") {
			t.Errorf("error %q should report the available line count", err)
		}
	})

	t.Run("grep filters and keeps original numbering", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 100, "ta$")
		if err != nil {
			t.Fatal(err)
		}
		want := "     2→beta"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("grep with no matches reports the searched count", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 100, "nope")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, `"nope"`) || !strings.Contains(got, "3 lines") {
			t.Errorf("got %q, want a no-matches message with the searched count", got)
		}
	})

	t.Run("invalid grep pattern errors", func(t *testing.T) {
		_, err := renderReadLogs("s", logLines(), 0, 100, "(")
		if err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("limit keeps the tail with a counted marker", func(t *testing.T) {
		got, err := renderReadLogs("s", logLines(), 0, 2, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "... 1 lines omitted (use ReadLogs(span: s) to read more) ...\n     2→beta\n     3→gamma"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
