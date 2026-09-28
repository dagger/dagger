package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/clientdb"
)

func traceTargetSpanID(id byte) dagui.SpanID {
	return dagui.SpanID{SpanID: trace.SpanID{id}}
}

func TestResolveTraceTargetSpanErrors(t *testing.T) {
	// Invalid arguments must be rejected before opening telemetry.
	for _, span := range []string{"", " ", "not-hex", "0000000000000000"} {
		_, err := resolveTraceTarget(t.Context(), span)
		if strings.TrimSpace(span) == "" {
			require.ErrorContains(t, err, "ReadTrace needs a span ID")
			require.ErrorContains(t, err, "FindSpans")
		} else {
			require.ErrorContains(t, err, "invalid span ID")
		}
	}
}

func TestResolveTraceTargetSpan(t *testing.T) {
	const spanID = "0000000000000001"
	const traceID = "000102030405060708090a0b0c0d0e0f"
	dbs := clientdb.NewDBs(t.TempDir())
	store, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	defer store.Close()
	_, err = store.AppendSpans([]clientdb.Span{{TraceID: traceID, SpanID: spanID, Name: "work"}})
	require.NoError(t, err)
	ctx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}})

	for _, arg := range []string{spanID, " span=" + spanID + " ", "[traceparent:" + traceID + "-" + spanID + "]", "00-" + traceID + "-" + spanID + "-01"} {
		got, err := resolveTraceTarget(ctx, arg)
		require.NoError(t, err)
		require.Equal(t, spanID, got)
	}
	_, err = resolveTraceTarget(ctx, "00000000000000ff")
	require.ErrorContains(t, err, "no span")
	require.ErrorContains(t, err, "FindSpans")
}

func TestReadTraceSpanOnlySchema(t *testing.T) {
	m := newMCP()
	tools := NewLLMToolSet()
	m.loadBuiltins(&dagql.Server{}, tools)
	tool, err := m.LookupTool("ReadTrace", tools.Order)
	require.NoError(t, err)
	require.Equal(t, []string{"span"}, tool.Schema["required"])
	require.Equal(t, false, tool.Schema["additionalProperties"])
	props := tool.Schema["properties"].(map[string]any)
	require.Contains(t, props, "span")
	require.NotContains(t, props, "check")
	require.NotContains(t, props, "test")
	require.Contains(t, tool.Description, "FindSpans first")
}

// Known check IDs are used directly, without a redundant name search.
func TestReadTraceRerunSuggestion(t *testing.T) {
	store, ids := traceInspectStore(t)
	session, err := loadTraceReportSession(t.Context(), store, ids["build"])
	require.NoError(t, err)
	db := session.DB()
	// Select the failed build, independent of ingestion order.
	for _, span := range db.Spans.Order {
		if span.ID.String() == ids["build"] {
			span.CheckName = "go:lint"
		}
	}
	heading, body := readTraceRerunSuggestion(db, []string{"go:lint"})
	require.Equal(t, "SEE FULL TRACE", heading)
	require.Equal(t, []string{`ReadTrace(span: "` + ids["build"] + `")`}, body)
}
