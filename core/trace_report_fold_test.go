package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/spanfootprint"
)

// footprintRow encodes a span as a store row, as the engine does.
func footprintRow(tb testing.TB, span sdktrace.ReadOnlySpan) clientdb.Span {
	tb.Helper()
	attrs, err := clientdb.MarshalKeyValueList(span.Attributes(), telemetryattrs.TelemetryOriginClientIDAttr)
	require.NoError(tb, err)
	links, err := clientdb.MarshalProtoList(telemetry.SpanLinksToPB(span.Links()))
	require.NoError(tb, err)
	resource, err := proto.Marshal(&otlpresourcev1.Resource{})
	require.NoError(tb, err)
	scope, err := proto.Marshal(&otlpcommonv1.InstrumentationScope{Name: "footprint"})
	require.NoError(tb, err)
	row := clientdb.Span{
		TraceID:              span.SpanContext().TraceID().String(),
		SpanID:               span.SpanContext().SpanID().String(),
		ParentSpanID:         sql.NullString{String: span.Parent().SpanID().String(), Valid: span.Parent().IsValid()},
		Name:                 span.Name(),
		Kind:                 "internal",
		StartTime:            span.StartTime().UnixNano(),
		Attributes:           attrs,
		Links:                links,
		StatusCode:           int64(span.Status().Code),
		StatusMessage:        span.Status().Description,
		InstrumentationScope: scope,
		Resource:             resource,
	}
	if !span.EndTime().Before(span.StartTime()) {
		row.EndTime = sql.NullInt64{Int64: span.EndTime().UnixNano(), Valid: true}
	}
	return row
}

// footprintStore is a store holding a spanfootprint session, live as the
// engine would store it (each span as it starts, again as it ends), with
// wait chains like the engine's on 6% of the spans, the nested-engine shape
// when nested, and display text on a tenth of the spans.
func footprintStore(tb testing.TB, n int, nested bool) (*clientdb.DB, *spanfootprint.Trace) {
	tb.Helper()
	tr := spanfootprint.New(n)
	store, err := clientdb.NewDBs(tb.TempDir()).Open(context.Background(), "footprint")
	require.NoError(tb, err)
	tb.Cleanup(func() { store.Close() })
	exp := &rowExporter{tb: tb, store: store}
	next := tr.WaitChains(exp)
	if nested {
		next = spanfootprint.Nested(next)
	}
	require.NoError(tb, tr.Export(context.Background(), next, 512, nil))
	stdout, err := clientdb.MarshalProtoList([]*otlpcommonv1.KeyValue{{
		Key:   telemetry.StdioStreamAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: 1}},
	}})
	require.NoError(tb, err)
	scope, err := proto.Marshal(&otlpcommonv1.InstrumentationScope{Name: "footprint"})
	require.NoError(tb, err)
	var logs []clientdb.Log
	for i := 0; i < n; i += 10 {
		body, err := proto.Marshal(stringLogBody(fmt.Sprintf("span %d says hi\n", i)))
		require.NoError(tb, err)
		logs = append(logs, clientdb.Log{
			TraceID:              sql.NullString{String: trace.TraceID{1}.String(), Valid: true},
			SpanID:               sql.NullString{String: tr.SpanID(i).String(), Valid: true},
			Body:                 body,
			Attributes:           stdout,
			InstrumentationScope: scope,
		})
	}
	_, err = store.AppendLogs(logs)
	require.NoError(tb, err)
	return store, tr
}

type rowExporter struct {
	tb    testing.TB
	store *clientdb.DB
}

func (e *rowExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	rows := make([]clientdb.Span, len(spans))
	for i, span := range spans {
		rows[i] = footprintRow(e.tb, span)
	}
	_, err := e.store.AppendSpans(rows)
	return err
}

func (e *rowExporter) Shutdown(context.Context) error { return nil }

// loadTraceReportSessionFull is loadTraceReportSession as it was before it
// folded: every span of the scope loaded in full. The reference a folded
// load must render identically to.
func loadTraceReportSessionFull(ctx context.Context, read *clientdb.DB, root string) (*idtui.ReportSession, error) {
	walk := read.SpanLogScope(root)
	session := idtui.NewReportSession(dagui.NewDB())
	if err := ingestSpanScope(ctx, read, session.DB(), read.AncestorClosure(walk)); err != nil {
		return nil, err
	}
	if missing := unreceivedErrorOrigins(session.DB()); len(missing) > 0 {
		if err := ingestSpanScope(ctx, read, session.DB(), read.AncestorClosure(missing)); err != nil {
			return nil, err
		}
	}
	logs, err := read.SelectLogsForSpans(ctx, walk, traceReportMaxLogRowsPerSpan)
	if err != nil {
		return nil, err
	}
	err = telemetry.ReexportLogsFromPB(ctx, session.LogExporter(), &collogspb.ExportLogsServiceRequest{
		ResourceLogs: clientdb.LogsToPB(logs),
	})
	return session, err
}

// TestTraceReportFoldMatchesFullLoad renders reports rooted at spans of
// every kind -- the session's root, an agent loop, tool calls, checks,
// plain work -- over a folding load and over a full one, and requires
// identical output: the same tree, roll-ups, logs and time breakdowns.
func TestTraceReportFoldMatchesFullLoad(t *testing.T) {
	ctx := t.Context()
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))
	const n = 6000
	for _, nested := range []bool{false, true} {
		name := "agent"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			store, tr := footprintStore(t, n, nested)
			full, err := loadTraceReportSessionFull(ctx, store, tr.SpanID(0).String())
			require.NoError(t, err)
			// roots: the session, an agent loop, and the first spans of
			// each kind the session has
			roots := []string{tr.SpanID(0).String(), tr.SpanID(1).String()}
			kinds := map[string]bool{}
			for _, span := range full.DB().Spans.Order {
				var kind string
				switch {
				case span.LLMTool != "":
					kind = "tool"
				case span.CheckName != "":
					kind = "check"
				case span.TestCaseName != "":
					kind = "test"
				case span.IsFailed():
					kind = "failed"
				case span.ChildSpans.Len() > 3:
					kind = "work"
				}
				if kind != "" && !kinds[kind] {
					kinds[kind] = true
					roots = append(roots, span.ID.String())
				}
			}
			var folded, total int
			for _, root := range roots {
				for optsName, opts := range map[string]traceReportOpts{
					"ReadTrace": readTraceReportOpts(),
					"tool":      toolCallReportOpts(),
					"plain":     {},
				} {
					want, err := loadTraceReportSessionFull(ctx, store, root)
					require.NoError(t, err)
					got, err := loadTraceReportSession(ctx, store, root, opts)
					require.NoError(t, err)
					wantReport, err := renderTraceReportSession(want, root, opts)
					require.NoError(t, err)
					gotReport, err := renderTraceReportSession(got, root, opts)
					require.NoError(t, err)
					require.Equal(t, wantReport, gotReport, "report on %s (%s)", root, optsName)
					stats := got.DB().Fold().Stats()
					folded += stats.Spans - stats.Retained
					total += stats.Spans
				}
			}
			require.NotZero(t, folded, "nothing folded")
			t.Logf("%d roots: %d of %d spans loaded folded (%.1f%%)", len(roots), folded, total, 100*float64(folded)/float64(total))
		})
	}
}
