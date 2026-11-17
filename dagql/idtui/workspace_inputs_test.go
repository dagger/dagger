package idtui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestWorkspaceInputsInspection(t *testing.T) {
	encode := func(record telemetryattrs.WorkspaceInputRecord) string {
		t.Helper()
		data, err := json.Marshal(record)
		require.NoError(t, err)
		return string(data)
	}
	var records []string
	for _, role := range []string{"read", "input", "retained"} {
		records = append(records, encode(telemetryattrs.WorkspaceInputRecord{
			Role: role, Workspace: "file:///src", Path: "/README.md",
			Include: []string{"**", "!README.md"}, Exclude: []string{"vendor/**"},
			Origin: "Workspace.directory", Input: []string{"receiver"}, Output: []string{"field:workspace"},
			Conservative: role == "input",
		}))
	}
	for i := 0; i < 5; i++ {
		records = append(records, encode(telemetryattrs.WorkspaceInputRecord{Role: "read", Workspace: "file:///src", Path: "/later"}))
	}
	attrs := []attribute.KeyValue{
		attribute.StringSlice(telemetryattrs.WorkspaceInputsAttr, records),
		attribute.StringSlice(telemetryattrs.WorkspaceInputsIssuesAttr, []string{"missing binding\n\x1b[31m"}),
		attribute.Bool(telemetryattrs.WorkspaceInputsCompleteAttr, false),
		attribute.Bool(telemetryattrs.WorkspaceInputsCapabilitiesCompleteAttr, true),
		attribute.Bool(telemetryattrs.WorkspaceInputsErrorAttr, false),
		attribute.Bool(telemetryattrs.WorkspaceInputsTruncatedAttr, true),
		attribute.Int(telemetryattrs.WorkspaceInputsOmittedAttr, 7),
	}

	newFrontend := func(attrs []attribute.KeyValue) *frontendPretty {
		t.Helper()
		db := dagui.NewDB()
		fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(100, 80))
		fe.profile = termenv.Ascii
		fe.logs.Profile = termenv.Ascii
		fe.GCThreshold = time.Hour
		start := time.Unix(100, 0)
		root := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}})
		child := trace.NewSpanContext(trace.SpanContextConfig{TraceID: root.TraceID(), SpanID: trace.SpanID{2}})
		payload, err := (&callpbv1.Call{Type: &callpbv1.Type{NamedType: "Directory"}, Field: "directory", Digest: "workspace-input-test"}).Encode()
		require.NoError(t, err)
		attrs = append(append([]attribute.KeyValue{}, attrs...),
			attribute.String(telemetry.DagCallAttr, payload),
			attribute.String(telemetry.DagDigestAttr, "workspace-input-test"),
			attribute.Bool(telemetry.UIInternalAttr, true))
		require.NoError(t, fe.SpanExporter().ExportSpans(context.Background(), tracetest.SpanStubs{
			{Name: "test query", SpanContext: root, StartTime: start, EndTime: start.Add(3 * time.Second)},
			{Name: "Workspace.directory", SpanContext: child, Parent: root, StartTime: start, EndTime: start.Add(2 * time.Second), Attributes: attrs},
		}.Snapshots()))
		fe.SetPrimary(dagui.SpanID{SpanID: root.SpanID()})
		fe.tui.Step() // dispatch the SDK exports and primary-span update
		return fe
	}
	fe := newFrontend(attrs)
	frame := func() string {
		fe.Update()
		return ansi.Strip(strings.Join(fe.tui.Step(), "\n"))
	}
	require.NotContains(t, frame(), "Workspace inputs")
	for fe.Verbosity < dagui.ShowInternalVerbosity {
		fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: '+', Text: "+"}))
	}
	callID := dagui.SpanID{SpanID: trace.SpanID{2}}
	fe.ExpandCompleted = true
	fe.recalculateViewLocked()
	fe.manualFocus(fe.rows.BySpan[callID])
	require.True(t, fe.rows.BySpan[callID].Expanded, "exercise automatic expansion")
	summary := frame()
	require.Contains(t, summary, "Workspace inputs: 6 Read, 1 Input, 1 Retained access")
	require.NotContains(t, summary, `path="/README.md"`, "automatic expansion must not dump path records")
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: uv.KeyLeft}))
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: uv.KeyRight}))
	require.True(t, fe.SpanExpanded[callID], "exercise deliberate expansion")
	live := frame()
	// Hard wrapping can split a token. Compare its complete text without the
	// display's line breaks and continuation indentation.
	compact := func(text string) string { return strings.Join(strings.Fields(text), "") }
	for _, text := range []string{"Workspace inputs", "not check selection", "Incomplete records. All paths may matter.", "Read:", "Input:", "Retained access:", `path="/README.md"`, `include=["**","!README.md"]`, `exclude=["vendor/**"]`, `reason="Workspace.directory"`, "conservative=true", "Telemetry limit: 7 records/issues omitted."} {
		require.Contains(t, compact(live), compact(text))
	}
	require.Equal(t, 5, strings.Count(live, `path="/later"`), "expanded records have no additional UI cap")
	require.NotContains(t, live, "Display limit")
	t.Log("Inline expanded:\n" + live)
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: uv.KeyLeft}))
	require.NotContains(t, frame(), `path="/README.md"`)
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: '?', Text: "?"}))
	raw := frame()
	require.Contains(t, raw, "ExtraAttributes")
	require.Contains(t, raw, telemetryattrs.WorkspaceInputsAttr)
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: '?', Text: "?"}))
	require.Empty(t, fe.progressExpanded, "workspace records must not use the transfer toggle")
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
	zoom := frame()
	require.Equal(t, 5, strings.Count(zoom, `path="/later"`))
	require.Contains(t, zoom, `path="/README.md"`)
	require.Equal(t, 1, strings.Count(zoom, "Workspace inputs (captured call records"))
	fe.finalRender = true
	final := frame()
	require.NotContains(t, final, "Display limit")
	require.Equal(t, 5, strings.Count(final, `path="/later"`))
	require.Contains(t, final, `Issue: "missing binding\n\x1b[31m"`)
	require.NotContains(t, final, "\x1b")
	t.Log("Span report:\n" + final)
	fe.finalRender = false
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: uv.KeyEscape}))
	require.NotContains(t, frame(), `path="/README.md"`)

	for _, tc := range []struct {
		name  string
		attrs []attribute.KeyValue
		want  string
	}{
		{"absent", nil, "No workspace input attributes received; reads are unknown."},
		{"partial", attrs[:1], "Recording status unavailable. All paths may matter."},
		{"error", append(attrs, attribute.Bool(telemetryattrs.WorkspaceInputsErrorAttr, true)), "Call failed; records may be partial. All paths may matter."},
		{"complete", []attribute.KeyValue{attribute.Bool(telemetryattrs.WorkspaceInputsCompleteAttr, true), attribute.Bool(telemetryattrs.WorkspaceInputsCapabilitiesCompleteAttr, true)}, "Source records complete; retained access complete."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := newFrontend(tc.attrs)
			other.FocusedSpan = dagui.SpanID{SpanID: trace.SpanID{2}}
			other.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
			other.finalRender = true
			other.Update()
			got := ansi.Strip(strings.Join(other.tui.Step(), "\n"))
			require.Contains(t, got, tc.want)
		})
	}
}
