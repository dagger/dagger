package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/vito/go-sse/sse"
)

type Topic struct {
	TraceID  trace.TraceID
	ClientID string
}

func (t Topic) String() string {
	return fmt.Sprintf("Topic{traceID=%s, clientID=%s}", t.TraceID, t.ClientID)
}

func telemetryOriginClientID(ctx context.Context, sessionID string) string {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || md.SessionID != sessionID {
		return ""
	}
	return md.ClientID
}

type telemetryOriginSpanProcessor struct {
	sessionID string
}

func (p telemetryOriginSpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if origin := telemetryOriginClientID(ctx, p.sessionID); origin != "" {
		span.SetAttributes(attribute.String(telemetryattrs.TelemetryOriginClientIDAttr, origin))
	}
}
func (telemetryOriginSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (telemetryOriginSpanProcessor) Shutdown(context.Context) error   { return nil }
func (telemetryOriginSpanProcessor) ForceFlush(context.Context) error { return nil }

type telemetryOriginLogProcessor struct {
	sessionID string
}

func (p telemetryOriginLogProcessor) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	if origin := telemetryOriginClientID(ctx, p.sessionID); origin != "" {
		rec.AddAttributes(log.String(telemetryattrs.TelemetryOriginClientIDAttr, origin))
	}
	return nil
}
func (telemetryOriginLogProcessor) Shutdown(context.Context) error   { return nil }
func (telemetryOriginLogProcessor) ForceFlush(context.Context) error { return nil }
func (telemetryOriginLogProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

func spanOriginClientID(span sdktrace.ReadOnlySpan) string {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == telemetryattrs.TelemetryOriginClientIDAttr && attr.Value.Type() == attribute.STRING {
			return attr.Value.AsString()
		}
	}
	return ""
}

func logOriginClientID(rec sdklog.Record) string {
	var origin string
	rec.WalkAttributes(func(attr log.KeyValue) bool {
		if attr.Key == telemetryattrs.TelemetryOriginClientIDAttr && attr.Value.Kind() == log.KindString {
			origin = attr.Value.AsString()
			return false
		}
		return true
	})
	return origin
}

type sessionSpanExporter struct {
	sess *daggerSession
	ps   *PubSub
}

func (exp sessionSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	byTarget := map[string][]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		origin := spanOriginClientID(span)
		if origin == "" {
			return fmt.Errorf("span %s is missing telemetry origin client ID", span.SpanContext().SpanID())
		}
		route, err := exp.sess.telemetryRouteOriginClientID(origin)
		if err != nil {
			return err
		}
		for _, target := range route {
			byTarget[target] = append(byTarget[target], span)
		}
	}
	var eg errgroup.Group
	for target, targetSpans := range byTarget {
		eg.Go(func() error {
			if err := exp.ps.Spans(target).ExportSpans(ctx, targetSpans); err != nil {
				return fmt.Errorf("export spans to %s: %w", target, err)
			}
			return nil
		})
	}
	return eg.Wait()
}
func (sessionSpanExporter) ForceFlush(context.Context) error { return nil }
func (sessionSpanExporter) Shutdown(context.Context) error   { return nil }

type sessionLogExporter struct {
	sess *daggerSession
	ps   *PubSub
}

func (exp sessionLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	byTarget := map[string][]sdklog.Record{}
	for _, rec := range records {
		origin := logOriginClientID(rec)
		if origin == "" {
			return fmt.Errorf("log record is missing telemetry origin client ID")
		}
		route, err := exp.sess.telemetryRouteOriginClientID(origin)
		if err != nil {
			return err
		}
		for _, target := range route {
			byTarget[target] = append(byTarget[target], rec)
		}
	}
	var eg errgroup.Group
	for target, targetRecords := range byTarget {
		eg.Go(func() error {
			if err := exp.ps.Logs(target).Export(ctx, targetRecords); err != nil {
				return fmt.Errorf("export logs to %s: %w", target, err)
			}
			return nil
		})
	}
	return eg.Wait()
}
func (sessionLogExporter) ForceFlush(context.Context) error { return nil }
func (sessionLogExporter) Shutdown(context.Context) error   { return nil }

// clientMetricExporter binds one live client's metric stream to its immutable
// record. Measurements therefore need no routing attribute: each provider
// aggregates one client's work, and export resolves that record's current
// origin-to-ancestor route without retaining any ancestor runtime.
type clientMetricExporter struct {
	record *clientRecord
	ps     *PubSub
}

func (exp clientMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (exp clientMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (exp clientMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	if metrics == nil || len(metrics.ScopeMetrics) == 0 {
		return nil
	}
	route, err := exp.record.daggerSession.telemetryRouteClientIDs(exp.record)
	if err != nil {
		return err
	}
	var eg errgroup.Group
	for _, target := range route {
		eg.Go(func() error {
			if err := exp.ps.Metrics(target).Export(ctx, metrics); err != nil {
				return fmt.Errorf("export metrics to %s: %w", target, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (clientMetricExporter) ForceFlush(context.Context) error { return nil }
func (clientMetricExporter) Shutdown(context.Context) error   { return nil }

// originSpanExporter and originLogExporter adapt telemetry delivered without an
// emission context (incoming OTLP and cloud scale-out) into the same stamped,
// session-owned routing path.
type originSpanExporter struct {
	origin string
	next   sdktrace.SpanExporter
}

type originReadOnlySpan struct {
	sdktrace.ReadOnlySpan
	attrs []attribute.KeyValue
}

func (span originReadOnlySpan) Attributes() []attribute.KeyValue { return span.attrs }

func withSpanOrigin(span sdktrace.ReadOnlySpan, origin string) sdktrace.ReadOnlySpan {
	attrs := make([]attribute.KeyValue, 0, len(span.Attributes())+1)
	for _, attr := range span.Attributes() {
		if string(attr.Key) != telemetryattrs.TelemetryOriginClientIDAttr {
			attrs = append(attrs, attr)
		}
	}
	attrs = append(attrs, attribute.String(telemetryattrs.TelemetryOriginClientIDAttr, origin))
	return originReadOnlySpan{ReadOnlySpan: span, attrs: attrs}
}

func (exp originSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	stamped := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		stamped[i] = withSpanOrigin(span, exp.origin)
	}
	return exp.next.ExportSpans(ctx, stamped)
}
func (originSpanExporter) ForceFlush(context.Context) error       { return nil }
func (exp originSpanExporter) Shutdown(ctx context.Context) error { return exp.next.Shutdown(ctx) }

type originLogExporter struct {
	origin string
	next   sdklog.Exporter
}

func (exp originLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	stamped := make([]sdklog.Record, len(records))
	for i := range records {
		stamped[i] = records[i].Clone()
		stamped[i].AddAttributes(log.String(telemetryattrs.TelemetryOriginClientIDAttr, exp.origin))
	}
	return exp.next.Export(ctx, stamped)
}
func (exp originLogExporter) ForceFlush(ctx context.Context) error { return exp.next.ForceFlush(ctx) }
func (exp originLogExporter) Shutdown(ctx context.Context) error   { return exp.next.Shutdown(ctx) }

type PubSub struct {
	srv *Server
	mux http.Handler
}

func NewPubSub(srv *Server) *PubSub {
	mux := http.NewServeMux()
	ps := &PubSub{
		srv: srv,
		mux: mux,
	}
	mux.HandleFunc("POST /v1/traces", ps.TracesHandler)
	mux.HandleFunc("POST /v1/logs", ps.LogsHandler)
	mux.HandleFunc("POST /v1/metrics", ps.MetricsHandler)
	return ps
}

func (ps *PubSub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ps.mux.ServeHTTP(w, r)
}

func (ps *PubSub) TracesHandler(rw http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	record, err := ps.srv.clientRecordFromIDs(sessionID, clientID)
	if err != nil {
		slog.Warn("error getting client", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling trace request", "payload", string(body), "error", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	spans := telemetry.SpansFromPB(req.ResourceSpans)
	slog.Debug("exporting spans", "spans", len(spans), "origin", clientID)

	start := time.Now()
	exporter := originSpanExporter{origin: clientID, next: record.daggerSession.spanExporter}
	if err := exporter.ExportSpans(r.Context(), spans); err != nil {
		slog.Error("error exporting spans", "err", err, "duration", time.Since(start))
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if elapsed := time.Since(start); elapsed > slowTelemetryOp {
		slog.Warn("slow span fan-out", "from", record.clientID, "spans", len(spans), "duration", elapsed)
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) LogsHandler(rw http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	record, err := ps.srv.clientRecordFromIDs(sessionID, clientID)
	if err != nil {
		slog.Warn("error getting client", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling logs request", "payload", string(body), "error", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	slog.Debug("exporting logs", "origin", clientID)

	start := time.Now()
	exporter := originLogExporter{origin: clientID, next: record.daggerSession.logExporter}
	if err := telemetry.ReexportLogsFromPB(r.Context(), exporter, &req); err != nil {
		slog.Error("error exporting logs", "err", err, "duration", time.Since(start))
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if elapsed := time.Since(start); elapsed > slowTelemetryOp {
		slog.Warn("slow log fan-out", "from", record.clientID, "duration", elapsed)
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) MetricsHandler(rw http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	record, err := ps.srv.clientRecordFromIDs(sessionID, clientID)
	if err != nil {
		slog.Warn("error getting client", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling metrics request", "payload", string(body), "error", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	slog.Debug("exporting metrics", "origin", clientID)

	start := time.Now()
	exporter := clientMetricExporter{record: record, ps: ps}
	if err := enginetel.ReexportMetricsFromPB(r.Context(), []sdkmetric.Exporter{exporter}, &req); err != nil {
		slog.Error("error exporting metrics", "err", err, "duration", time.Since(start))
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if elapsed := time.Since(start); elapsed > slowTelemetryOp {
		slog.Warn("slow metric fan-out", "from", record.clientID, "duration", elapsed)
	}

	rw.WriteHeader(http.StatusCreated)
}

const otlpBatchSize = 1000

// slowTelemetryOp flags telemetry DB operations slow enough to threaten a
// client's shutdown budget (the CLI allows 10s for the whole shutdown drain).
const slowTelemetryOp = 1 * time.Second

// logTelemetryWrite records how a client-DB write batch of N rows spent its
// time. appendDuration is the in-memory append including any hard-cap wait;
// capWaitDuration isolates that backpressure, while spillLag reports the tail
// waiting for the background spiller when Append returned.
func logTelemetryWrite(clientID, what string, rows int, totalStart, appendStart time.Time, stats clientdb.AppendStats, err error) {
	total := time.Since(totalStart)
	lg := slog.With(
		"client", clientID,
		"what", what,
		"rows", rows,
		"duration", total,
		"appendDuration", time.Since(appendStart),
		"capWaitDuration", stats.CapWaitDuration,
		"capWaitEngaged", stats.CapWaitDuration > 0,
		"spillLagRows", stats.SpillLagRows,
		"spillLagBytes", stats.SpillLagBytes,
		"error", err,
	)
	switch {
	case total > slowTelemetryOp:
		lg.Warn("slow client DB telemetry write")
	case total > 100*time.Millisecond || rows >= 100:
		lg.Debug("client DB telemetry write")
	default:
		lg.ExtraDebug("client DB telemetry write")
	}
}

func (ps *PubSub) TracesSubscribeHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	return ps.sseHandler(w, r, record, func(ctx context.Context, db *clientdb.DB, lastID string) (*sse.Event, bool, error) {
		var since int64
		if lastID != "" {
			_, err := fmt.Sscanf(lastID, "%d", &since)
			if err != nil {
				return nil, false, fmt.Errorf("invalid last ID: %w", err)
			}
		}
		spans, err := db.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{
			ID:    since,
			Limit: otlpBatchSize,
		})
		if err != nil {
			return nil, false, fmt.Errorf("select spans: %w", err)
		}
		if len(spans) == 0 {
			return nil, false, nil
		}
		roSpans := make([]sdktrace.ReadOnlySpan, len(spans))
		for i, span := range spans {
			roSpans[i] = span.ReadOnly()
			since = span.ID
		}
		// Marshal the spans to OTLP.
		payload, err := protojson.Marshal(&coltracepb.ExportTraceServiceRequest{
			ResourceSpans: telemetry.SpansToPB(roSpans),
		})
		if err != nil {
			return nil, false, fmt.Errorf("marshal spans: %w", err)
		}
		return &sse.Event{
			Name: "spans",
			ID:   fmt.Sprintf("%d", since),
			Data: payload,
		}, true, nil
	})
}

//nolint:dupl
func (ps *PubSub) LogsSubscribeHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	return ps.sseHandler(w, r, record, func(ctx context.Context, db *clientdb.DB, lastID string) (*sse.Event, bool, error) {
		var since int64
		if lastID != "" {
			_, err := fmt.Sscanf(lastID, "%d", &since)
			if err != nil {
				return nil, false, fmt.Errorf("invalid last ID: %w", err)
			}
		}
		logs, err := db.Read().SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{
			ID:    since,
			Limit: otlpBatchSize,
		})
		if err != nil {
			return nil, false, fmt.Errorf("select logs: %w", err)
		}
		if len(logs) == 0 {
			return nil, false, nil
		}
		since = logs[len(logs)-1].ID
		// Marshal the logs to OTLP.
		payload, err := protojson.Marshal(&collogspb.ExportLogsServiceRequest{
			ResourceLogs: clientdb.LogsToPB(logs),
		})
		if err != nil {
			return nil, false, fmt.Errorf("marshal logs: %w", err)
		}
		return &sse.Event{
			Name: "logs",
			ID:   fmt.Sprintf("%d", since),
			Data: payload,
		}, true, nil
	})
}

//nolint:dupl
func (ps *PubSub) MetricsSubscribeHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	return ps.sseHandler(w, r, record, func(ctx context.Context, db *clientdb.DB, lastID string) (*sse.Event, bool, error) {
		var since int64
		if lastID != "" {
			_, err := fmt.Sscanf(lastID, "%d", &since)
			if err != nil {
				return nil, false, fmt.Errorf("invalid last ID: %w", err)
			}
		}
		metrics, err := db.Read().SelectMetricsSince(ctx, clientdb.SelectMetricsSinceParams{
			ID:    since,
			Limit: otlpBatchSize,
		})
		if err != nil {
			return nil, false, fmt.Errorf("select metrics: %w", err)
		}

		if len(metrics) == 0 {
			return nil, false, nil
		}
		since = metrics[len(metrics)-1].ID
		// Marshal the metrics to OTLP.
		payload, err := protojson.Marshal(&colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: clientdb.MetricsToPB(metrics),
		})
		if err != nil {
			return nil, false, fmt.Errorf("marshal metrics: %w", err)
		}
		return &sse.Event{
			Name: "metrics",
			ID:   fmt.Sprintf("%d", since),
			Data: payload,
		}, true, nil
	})
}

type clientSpans struct {
	*PubSub
	clientID string
}

func (ps *PubSub) Spans(clientID string) sdktrace.SpanExporter {
	return clientSpans{
		PubSub:   ps,
		clientID: clientID,
	}
}

func (ps clientSpans) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	slog.ExtraDebug("pubsub exporting spans", "client", ps.clientID, "count", len(spans))
	start := time.Now()

	var inserts []clientdb.Span
	for _, span := range spans {
		traceID := span.SpanContext().TraceID().String()
		spanID := span.SpanContext().SpanID().String()
		traceState := span.SpanContext().TraceState().String()
		parentSpanID := span.Parent().SpanID().String()
		flags := int64(span.SpanContext().TraceFlags())
		name := span.Name()
		kind := span.SpanKind().String()
		startTime := span.StartTime().UnixNano()
		endTime := sql.NullInt64{
			Int64: span.EndTime().UnixNano(),
			Valid: !span.EndTime().IsZero(),
		}
		if span.EndTime().Before(span.StartTime()) {
			endTime.Int64 = 0
			endTime.Valid = false
		}
		attributes, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues(span.Attributes()))
		if err != nil {
			slog.Warn("failed to marshal attributes", "error", err)
			continue
		}
		droppedAttributesCount := int64(span.DroppedAttributes())
		events, err := clientdb.MarshalProtoJSONs(telemetry.SpanEventsToPB(span.Events()))
		if err != nil {
			slog.Warn("failed to marshal events", "error", err)
			continue
		}
		droppedEventsCount := int64(span.DroppedEvents())
		links, err := clientdb.MarshalProtoJSONs(telemetry.SpanLinksToPB(span.Links()))
		if err != nil {
			slog.Warn("failed to marshal links", "error", err)
			continue
		}
		droppedLinksCount := int64(span.DroppedLinks())
		statusCode := int64(span.Status().Code)
		statusMessage := span.Status().Description
		instrumentationScope, err := protojson.Marshal(telemetry.InstrumentationScopeToPB(span.InstrumentationScope()))
		if err != nil {
			slog.Warn("failed to marshal instrumentation scope", "error", err)
			continue
		}
		resource, err := protojson.Marshal(telemetry.ResourcePtrToPB(span.Resource()))
		if err != nil {
			slog.Warn("failed to marshal resource", "error", err)
			continue
		}

		inserts = append(inserts, clientdb.Span{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceState: traceState,
			ParentSpanID: sql.NullString{
				String: parentSpanID,
				Valid:  span.Parent().IsValid(),
			},
			Flags:                  flags,
			Name:                   name,
			Kind:                   kind,
			StartTime:              startTime,
			EndTime:                endTime,
			Attributes:             attributes,
			DroppedAttributesCount: droppedAttributesCount,
			Events:                 events,
			DroppedEventsCount:     droppedEventsCount,
			Links:                  links,
			DroppedLinksCount:      droppedLinksCount,
			StatusCode:             statusCode,
			StatusMessage:          statusMessage,
			InstrumentationScope:   instrumentationScope,
			Resource:               resource,
		})
	}

	db, err := ps.srv.clientDBs.Open(ctx, ps.clientID)
	if err != nil {
		return fmt.Errorf("get telemetry db: %w", err)
	}
	defer db.Close()

	appendStart := time.Now()
	stats, appendErr := db.AppendSpans(inserts)
	logTelemetryWrite(ps.clientID, "spans", len(inserts), start, appendStart, stats, appendErr)
	if appendErr != nil {
		return appendErr
	}

	return nil
}

func (ps clientSpans) ForceFlush(ctx context.Context) error { return nil }
func (ps clientSpans) Shutdown(context.Context) error       { return nil }

func (ps *PubSub) Logs(clientID string) sdklog.Exporter {
	return clientLogs{
		PubSub:   ps,
		clientID: clientID,
	}
}

type clientLogs struct {
	*PubSub
	clientID string
}

var _ sdklog.Exporter = clientLogs{}

func (ps clientLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	slog.ExtraDebug("pubsub exporting logs", "client", ps.clientID, "count", len(logs))
	start := time.Now()

	var inserts []clientdb.Log
	for _, rec := range logs {
		insert, err := logRecordRow(&rec)
		if err != nil {
			return fmt.Errorf("prepare log record %v: %w", rec, err)
		}
		inserts = append(inserts, insert)
	}

	db, err := ps.srv.clientDBs.Open(ctx, ps.clientID)
	if err != nil {
		return fmt.Errorf("get telemetry db: %w", err)
	}
	defer db.Close()

	appendStart := time.Now()
	stats, appendErr := db.AppendLogs(inserts)
	logTelemetryWrite(ps.clientID, "logs", len(inserts), start, appendStart, stats, appendErr)
	if appendErr != nil {
		// Log export remains best-effort, but the append-only store's I/O
		// failures apply to the entire batch rather than an individual row.
		slog.Warn("failed to append log records", "error", appendErr)
	}

	return nil
}

func (ps clientLogs) ForceFlush(ctx context.Context) error { return nil }
func (ps clientLogs) Shutdown(context.Context) error       { return nil }

func logRecordRow(rec *sdklog.Record) (clientdb.Log, error) {
	traceID := rec.TraceID().String()
	spanID := rec.SpanID().String()
	timestamp := rec.Timestamp().UnixNano()
	severity := int64(rec.Severity())

	var body []byte
	if !rec.Body().Empty() {
		var err error
		body, err = proto.Marshal(telemetry.LogValueToPB(rec.Body()))
		if err != nil {
			return clientdb.Log{}, fmt.Errorf("marshal log record body: %w", err)
		}
	}

	attrs := []*otlpcommonv1.KeyValue{}
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		attrs = append(attrs, &otlpcommonv1.KeyValue{
			Key:   kv.Key,
			Value: telemetry.LogValueToPB(kv.Value),
		})
		return true
	})
	attributes, err := clientdb.MarshalProtoJSONs(attrs)
	if err != nil {
		return clientdb.Log{}, fmt.Errorf("marshal log record attributes: %w", err)
	}

	scope, err := protojson.Marshal(telemetry.InstrumentationScopeToPB(rec.InstrumentationScope()))
	if err != nil {
		return clientdb.Log{}, fmt.Errorf("marshal log record instrumentation scope: %w", err)
	}

	res := rec.Resource()
	resource, err := protojson.Marshal(telemetry.ResourcePtrToPB(res))
	if err != nil {
		return clientdb.Log{}, fmt.Errorf("marshal log record resource: %w", err)
	}

	return clientdb.Log{
		TraceID: sql.NullString{
			String: traceID,
			Valid:  rec.TraceID().IsValid(),
		},
		SpanID: sql.NullString{
			String: spanID,
			Valid:  rec.SpanID().IsValid(),
		},
		Timestamp:            timestamp,
		SeverityNumber:       severity,
		SeverityText:         rec.SeverityText(),
		Body:                 body,
		Attributes:           attributes,
		InstrumentationScope: scope,
		Resource:             resource,
		ResourceSchemaURL:    res.SchemaURL(),
	}, nil
}

func (ps *PubSub) Metrics(clientID string) sdkmetric.Exporter {
	return clientMetrics{
		PubSub:   ps,
		clientID: clientID,
	}
}

type clientMetrics struct {
	*PubSub
	clientID string
}

func (ps clientMetrics) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	if len(metrics.ScopeMetrics) == 0 {
		return nil
	}

	slog.ExtraDebug("pubsub exporting metrics", "client", ps.clientID, "count", len(metrics.ScopeMetrics))
	start := time.Now()

	pbMetrics, err := telemetry.ResourceMetricsToPB(metrics)
	if err != nil {
		return fmt.Errorf("convert metrics to pb: %w", err)
	}

	metricsPBBytes, err := protojson.Marshal(pbMetrics)
	if err != nil {
		return fmt.Errorf("marshal metrics to pb: %w", err)
	}

	db, err := ps.srv.clientDBs.Open(ctx, ps.clientID)
	if err != nil {
		return fmt.Errorf("get telemetry db: %w", err)
	}
	defer db.Close()

	appendStart := time.Now()
	stats, err := db.AppendMetrics([]clientdb.Metric{{Data: metricsPBBytes}})
	logTelemetryWrite(ps.clientID, "metrics", 1, start, appendStart, stats, err)
	if err != nil {
		return fmt.Errorf("append metrics: %w", err)
	}

	return nil
}

func (ps clientMetrics) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (ps clientMetrics) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (ps clientMetrics) ForceFlush(ctx context.Context) error { return nil }
func (ps clientMetrics) Shutdown(context.Context) error       { return nil }

type Fetcher func(ctx context.Context, db *clientdb.DB, since string) (*sse.Event, bool, error)

func (ps *PubSub) sseHandler(w http.ResponseWriter, r *http.Request, record *clientRecord, fetcher Fetcher) error {
	slog := slog.With("client", record.clientID, "path", r.URL.Path)

	flush := func() {
		slog.Warn("flush not supported?")
	}
	if flusher, ok := w.(http.Flusher); ok {
		flush = flusher.Flush
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	since := r.Header.Get("X-Last-Event-ID")

	db, err := record.TelemetryDB(r.Context())
	if err != nil {
		return fmt.Errorf("open client db: %w", err)
	}
	defer db.Close()

	// Send an initial event just to indicate that the client has subscribed.
	//
	// This helps distinguish 'attached but no data yet' vs. 'waiting for headers'.
	// Theoretically the flush() is enough, but we might as well send a different
	// event type to keep people on their toes.
	sse.Event{
		Name: "subscribed",
	}.Write(w)
	flush()

	var terminating bool
	for {
		fetchStart := time.Now()
		event, hasData, err := fetcher(r.Context(), db, since)
		if elapsed := time.Since(fetchStart); elapsed > slowTelemetryOp {
			// A slow historical file scan does not hold the stream mutex, but it
			// can still threaten the terminating subscriber's drain budget.
			slog.Warn("slow SSE fetch", "duration", elapsed, "hasData", hasData, "error", err)
		}
		if err != nil {
			slog.Warn("error fetching event", "err", err)
			return fmt.Errorf("fetch: %w", err)
		}
		if !hasData {
			if terminating {
				// We're already terminating and found no data, so we're done.
				return nil
			}
			select {
			case <-time.After(telemetry.NearlyImmediate):
				// Poll for more data at the same frequency that it's batched and saved.
				// Tail reads are cheap enough for aggressive polling.
				// Synchronizing with writes isn't worth the accompanying risk of hangs.
				//
				// NB: logging here is a bit too crazy
			case <-record.shutdownCh:
				// Client is shutting down; next time we receive no data, we'll exit.
				slog.ExtraDebug("shutting down")
				terminating = true
			case <-r.Context().Done():
				// Client went away, no point hanging around.
				slog.ExtraDebug("client went away")
				return nil
			}
			continue
		}

		since = event.ID

		if err := event.Write(w); err != nil {
			return fmt.Errorf("write: %w", err)
		}

		flush()
	}
}
