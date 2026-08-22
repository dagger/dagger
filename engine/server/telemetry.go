package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/dagql/call/callpbv1"
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

func withoutSpanOrigin(span sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	attrs := make([]attribute.KeyValue, 0, len(span.Attributes()))
	for _, attr := range span.Attributes() {
		if string(attr.Key) != telemetryattrs.TelemetryOriginClientIDAttr {
			attrs = append(attrs, attr)
		}
	}
	return originReadOnlySpan{ReadOnlySpan: span, attrs: attrs}
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

func withoutLogOrigin(rec sdklog.Record) sdklog.Record {
	clean := rec.Clone()
	attrs := make([]log.KeyValue, 0, rec.AttributesLen())
	rec.WalkAttributes(func(attr log.KeyValue) bool {
		if attr.Key != telemetryattrs.TelemetryOriginClientIDAttr {
			attrs = append(attrs, attr)
		}
		return true
	})
	clean.SetAttributes(attrs...)
	return clean
}

// classifyCallPayloadRecord identifies the call-payload log channel and
// returns the payload's embedded recipe digest. Any record whose content type
// declares an encoded call belongs to this channel, even when malformed, so it
// can never fall through as an ordinary log record.
func classifyCallPayloadRecord(rec sdklog.Record) (digest string, payload bool, err error) {
	claimed := false
	rec.WalkAttributes(func(attr log.KeyValue) bool {
		if attr.Key == telemetry.ContentTypeAttr {
			claimed = attr.Value.Kind() == log.KindString &&
				attr.Value.AsString() == telemetryattrs.CallPayloadContentType
			return false
		}
		return true
	})
	if !claimed {
		return "", false, nil
	}
	if rec.Body().Kind() != log.KindBytes {
		return "", true, fmt.Errorf("body must be bytes, got %s", rec.Body().Kind())
	}
	decoded := new(callpbv1.Call)
	if err := proto.Unmarshal(rec.Body().AsBytes(), decoded); err != nil {
		return "", true, fmt.Errorf("decode call payload: %w", err)
	}
	if decoded.GetDigest() == "" {
		return "", true, fmt.Errorf("missing embedded digest")
	}
	return decoded.GetDigest(), true, nil
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
		span = withoutSpanOrigin(span)
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
		digest, payload, err := classifyCallPayloadRecord(rec)
		if err != nil {
			slog.Warn("dropping malformed call payload record", "err", err)
			continue
		}

		origin := logOriginClientID(rec)
		if origin == "" {
			return fmt.Errorf("log record is missing telemetry origin client ID")
		}
		route, err := exp.sess.telemetryRouteOriginClientID(origin)
		if err != nil {
			return err
		}
		if payload {
			route = exp.sess.callPayloadMissingTargets(digest, route, true)
		}
		if len(route) == 0 {
			continue
		}
		rec = withoutLogOrigin(rec)
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

type sessionMetricExporter struct {
	sess *daggerSession
	ps   *PubSub
}

func (exp sessionMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (exp sessionMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (exp sessionMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	if metrics == nil || len(metrics.ScopeMetrics) == 0 {
		return nil
	}
	origins := map[string]struct{}{}
	_, err := transformResourceMetrics(metrics, func(attrs attribute.Set) (attribute.Set, bool, error) {
		origin, err := metricDataOriginClientID(attrs)
		if err != nil {
			return attrs, false, err
		}
		origins[origin] = struct{}{}
		return attrs, true, nil
	})
	if err != nil {
		return err
	}

	originsByTarget := map[string]map[string]struct{}{}
	for origin := range origins {
		route, err := exp.sess.telemetryRouteOriginClientID(origin)
		if err != nil {
			return err
		}
		for _, target := range route {
			if originsByTarget[target] == nil {
				originsByTarget[target] = map[string]struct{}{}
			}
			originsByTarget[target][origin] = struct{}{}
		}
	}

	var eg errgroup.Group
	for target, targetOrigins := range originsByTarget {
		routed, err := transformResourceMetrics(metrics, func(attrs attribute.Set) (attribute.Set, bool, error) {
			origin, err := metricDataOriginClientID(attrs)
			if err != nil {
				return attrs, false, err
			}
			_, include := targetOrigins[origin]
			filtered, _ := attrs.Filter(func(attr attribute.KeyValue) bool {
				return string(attr.Key) != telemetryattrs.TelemetryOriginClientIDAttr
			})
			return filtered, include, nil
		})
		if err != nil {
			return err
		}
		eg.Go(func() error {
			if err := exp.ps.Metrics(target).Export(ctx, routed); err != nil {
				return fmt.Errorf("export metrics to %s: %w", target, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (sessionMetricExporter) ForceFlush(context.Context) error { return nil }
func (sessionMetricExporter) Shutdown(context.Context) error   { return nil }

func metricDataOriginClientID(attrs attribute.Set) (string, error) {
	value, ok := attrs.Value(attribute.Key(telemetryattrs.TelemetryOriginClientIDAttr))
	if !ok || value.Type() != attribute.STRING || value.AsString() == "" {
		return "", fmt.Errorf("metric data point is missing telemetry origin client ID")
	}
	return value.AsString(), nil
}

type metricAttributeTransform func(attribute.Set) (attribute.Set, bool, error)

func transformResourceMetrics(metrics *metricdata.ResourceMetrics, transform metricAttributeTransform) (*metricdata.ResourceMetrics, error) {
	out := &metricdata.ResourceMetrics{Resource: metrics.Resource}
	for _, scopeMetrics := range metrics.ScopeMetrics {
		outScope := metricdata.ScopeMetrics{Scope: scopeMetrics.Scope}
		for _, data := range scopeMetrics.Metrics {
			transformed, include, err := transformMetric(data, transform)
			if err != nil {
				return nil, fmt.Errorf("transform metric %q: %w", data.Name, err)
			}
			if include {
				outScope.Metrics = append(outScope.Metrics, transformed)
			}
		}
		if len(outScope.Metrics) > 0 {
			out.ScopeMetrics = append(out.ScopeMetrics, outScope)
		}
	}
	return out, nil
}

func transformMetric(data metricdata.Metrics, transform metricAttributeTransform) (metricdata.Metrics, bool, error) {
	var err error
	switch aggregation := data.Data.(type) {
	case metricdata.Gauge[int64]:
		aggregation.DataPoints, err = transformDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.Gauge[float64]:
		aggregation.DataPoints, err = transformDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.Sum[int64]:
		aggregation.DataPoints, err = transformDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.Sum[float64]:
		aggregation.DataPoints, err = transformDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.Histogram[int64]:
		aggregation.DataPoints, err = transformHistogramDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.Histogram[float64]:
		aggregation.DataPoints, err = transformHistogramDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.ExponentialHistogram[int64]:
		aggregation.DataPoints, err = transformExponentialHistogramDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.ExponentialHistogram[float64]:
		aggregation.DataPoints, err = transformExponentialHistogramDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	case metricdata.Summary:
		aggregation.DataPoints, err = transformSummaryDataPoints(aggregation.DataPoints, transform)
		data.Data = aggregation
	default:
		return metricdata.Metrics{}, false, fmt.Errorf("unsupported aggregation %T", data.Data)
	}
	if err != nil {
		return metricdata.Metrics{}, false, err
	}
	return data, metricDataPointCount(data.Data) > 0, nil
}

func transformDataPoints[N int64 | float64](points []metricdata.DataPoint[N], transform metricAttributeTransform) ([]metricdata.DataPoint[N], error) {
	out := make([]metricdata.DataPoint[N], 0, len(points))
	for _, point := range points {
		attrs, include, err := transform(point.Attributes)
		if err != nil {
			return nil, err
		}
		if include {
			point.Attributes = attrs
			out = append(out, point)
		}
	}
	return out, nil
}

func transformHistogramDataPoints[N int64 | float64](points []metricdata.HistogramDataPoint[N], transform metricAttributeTransform) ([]metricdata.HistogramDataPoint[N], error) {
	out := make([]metricdata.HistogramDataPoint[N], 0, len(points))
	for _, point := range points {
		attrs, include, err := transform(point.Attributes)
		if err != nil {
			return nil, err
		}
		if include {
			point.Attributes = attrs
			out = append(out, point)
		}
	}
	return out, nil
}

func transformExponentialHistogramDataPoints[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N], transform metricAttributeTransform) ([]metricdata.ExponentialHistogramDataPoint[N], error) {
	out := make([]metricdata.ExponentialHistogramDataPoint[N], 0, len(points))
	for _, point := range points {
		attrs, include, err := transform(point.Attributes)
		if err != nil {
			return nil, err
		}
		if include {
			point.Attributes = attrs
			out = append(out, point)
		}
	}
	return out, nil
}

func transformSummaryDataPoints(points []metricdata.SummaryDataPoint, transform metricAttributeTransform) ([]metricdata.SummaryDataPoint, error) {
	out := make([]metricdata.SummaryDataPoint, 0, len(points))
	for _, point := range points {
		attrs, include, err := transform(point.Attributes)
		if err != nil {
			return nil, err
		}
		if include {
			point.Attributes = attrs
			out = append(out, point)
		}
	}
	return out, nil
}

func metricDataPointCount(data metricdata.Aggregation) int {
	switch aggregation := data.(type) {
	case metricdata.Gauge[int64]:
		return len(aggregation.DataPoints)
	case metricdata.Gauge[float64]:
		return len(aggregation.DataPoints)
	case metricdata.Sum[int64]:
		return len(aggregation.DataPoints)
	case metricdata.Sum[float64]:
		return len(aggregation.DataPoints)
	case metricdata.Histogram[int64]:
		return len(aggregation.DataPoints)
	case metricdata.Histogram[float64]:
		return len(aggregation.DataPoints)
	case metricdata.ExponentialHistogram[int64]:
		return len(aggregation.DataPoints)
	case metricdata.ExponentialHistogram[float64]:
		return len(aggregation.DataPoints)
	case metricdata.Summary:
		return len(aggregation.DataPoints)
	default:
		return 0
	}
}

// originSpanExporter, originLogExporter, and originMetricExporter adapt
// telemetry delivered without an emission context (incoming OTLP and cloud
// scale-out) into the same stamped, session-owned routing path.
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

type originMetricExporter struct {
	origin string
	next   sdkmetric.Exporter
}

func (exp originMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exp.next.Temporality(kind)
}

func (exp originMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exp.next.Aggregation(kind)
}

func (exp originMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	if metrics == nil {
		return exp.next.Export(ctx, nil)
	}
	stamped, err := transformResourceMetrics(metrics, func(attrs attribute.Set) (attribute.Set, bool, error) {
		filtered, _ := attrs.Filter(func(attr attribute.KeyValue) bool {
			return string(attr.Key) != telemetryattrs.TelemetryOriginClientIDAttr
		})
		originAttrs := append(filtered.ToSlice(), attribute.String(telemetryattrs.TelemetryOriginClientIDAttr, exp.origin))
		return attribute.NewSet(originAttrs...), true, nil
	})
	if err != nil {
		return err
	}
	return exp.next.Export(ctx, stamped)
}

func (exp originMetricExporter) ForceFlush(ctx context.Context) error {
	return exp.next.ForceFlush(ctx)
}

func (exp originMetricExporter) Shutdown(ctx context.Context) error {
	return exp.next.Shutdown(ctx)
}

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

	route, err := record.daggerSession.telemetryRouteClientIDs(record)
	if err != nil {
		slog.Warn("error resolving telemetry route", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Debug("exporting metrics to clients", "clients", len(route))

	start := time.Now()
	exporter := originMetricExporter{origin: clientID, next: record.daggerSession.metricExporter}
	if err := enginetel.ReexportMetricsFromPB(r.Context(), []sdkmetric.Exporter{exporter}, &req); err != nil {
		slog.Error("error exporting metrics", "err", err, "duration", time.Since(start))
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if elapsed := time.Since(start); elapsed > slowTelemetryOp {
		slog.Warn("slow metric fan-out", "from", record.clientID, "clients", len(route), "duration", elapsed)
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
	return ps.streamHandler(w, r, record, func(ctx context.Context, db *clientdb.DB, since int64, limit int) (int64, proto.Message, int, error) {
		spans, err := db.Read().SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{
			ID:    since,
			Limit: int64(limit),
		})
		if err != nil {
			return 0, nil, 0, fmt.Errorf("select spans: %w", err)
		}
		if len(spans) == 0 {
			return since, nil, 0, nil
		}
		roSpans := make([]sdktrace.ReadOnlySpan, len(spans))
		for i, span := range spans {
			roSpans[i] = span.ReadOnly()
			since = span.ID
		}
		return since, &coltracepb.ExportTraceServiceRequest{
			ResourceSpans: telemetry.SpansToPB(roSpans),
		}, len(spans), nil
	})
}

//nolint:dupl
func (ps *PubSub) LogsSubscribeHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	return ps.streamHandler(w, r, record, func(ctx context.Context, db *clientdb.DB, since int64, limit int) (int64, proto.Message, int, error) {
		logs, err := db.Read().SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{
			ID:    since,
			Limit: int64(limit),
		})
		if err != nil {
			return 0, nil, 0, fmt.Errorf("select logs: %w", err)
		}
		if len(logs) == 0 {
			return since, nil, 0, nil
		}
		since = logs[len(logs)-1].ID
		return since, &collogspb.ExportLogsServiceRequest{
			ResourceLogs: clientdb.LogsToPB(logs),
		}, len(logs), nil
	})
}

//nolint:dupl
func (ps *PubSub) MetricsSubscribeHandler(w http.ResponseWriter, r *http.Request, record *clientRecord) error {
	return ps.streamHandler(w, r, record, func(ctx context.Context, db *clientdb.DB, since int64, limit int) (int64, proto.Message, int, error) {
		metrics, err := db.Read().SelectMetricsSince(ctx, clientdb.SelectMetricsSinceParams{
			ID:    since,
			Limit: int64(limit),
		})
		if err != nil {
			return 0, nil, 0, fmt.Errorf("select metrics: %w", err)
		}
		if len(metrics) == 0 {
			return since, nil, 0, nil
		}
		since = metrics[len(metrics)-1].ID
		return since, &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: clientdb.MetricsToPB(metrics),
		}, len(metrics), nil
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

type streamFetcher func(ctx context.Context, db *clientdb.DB, since int64, limit int) (next int64, message proto.Message, rows int, err error)

func (ps *PubSub) streamHandler(w http.ResponseWriter, r *http.Request, record *clientRecord, fetcher streamFetcher) error {
	return ps.streamHandlerWithPayloadLimit(w, r, record, fetcher, enginetel.MaxLivePayloadSize)
}

func acceptsBinaryTelemetry(accept string) bool {
	for _, value := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil || mediaType != enginetel.LiveContentType {
			continue
		}
		if quality, ok := params["q"]; ok {
			q, err := strconv.ParseFloat(quality, 64)
			if err != nil || q <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

func legacyTelemetryEventName(path string) string {
	if path == "/v1/traces" {
		return "spans"
	}
	return strings.TrimPrefix(path, "/v1/")
}

func (ps *PubSub) streamHandlerWithPayloadLimit(w http.ResponseWriter, r *http.Request, record *clientRecord, fetcher streamFetcher, maxPayloadSize int) error {
	logger := slog.With("client", record.clientID, "path", r.URL.Path)
	if maxPayloadSize <= 0 || maxPayloadSize > enginetel.MaxLivePayloadSize {
		return fmt.Errorf("invalid live telemetry payload limit %d", maxPayloadSize)
	}
	binary := acceptsBinaryTelemetry(r.Header.Get("Accept"))

	var flush func()
	if flusher, ok := w.(http.Flusher); ok {
		flush = flusher.Flush
	} else {
		flush = func() { logger.Warn("response flushing is not supported") }
	}

	cursorHeader := enginetel.LegacyLiveCursorHeader
	if binary {
		cursorHeader = enginetel.LiveCursorHeader
	}
	cursor := r.Header.Get(cursorHeader)
	if !binary && cursor == "" {
		cursor = r.Header.Get("Last-Event-ID")
	}
	var since int64
	if cursor != "" {
		var err error
		since, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || since < 0 {
			return fmt.Errorf("invalid telemetry cursor %q", cursor)
		}
	}

	db, err := record.TelemetryDB(r.Context())
	if err != nil {
		return fmt.Errorf("open client db: %w", err)
	}
	defer db.Close()

	w.Header().Set("Cache-Control", "no-cache")
	if binary {
		w.Header().Set("Content-Type", enginetel.LiveContentType)
	} else {
		w.Header().Set("Content-Type", enginetel.LegacyLiveContentType)
		w.Header().Set("Connection", "keep-alive")
	}
	w.WriteHeader(http.StatusOK)
	// Commit and flush the response before waiting for the first batch so the
	// client can distinguish an attached subscription from pending headers.
	if !binary {
		if err := (sse.Event{Name: "subscribed"}).Write(w); err != nil {
			return fmt.Errorf("write subscribed event: %w", err)
		}
	}
	flush()

	terminating := false
	batchLimit := otlpBatchSize
	failStream := func(streamErr error) error {
		logger.Error("terminating OTLP stream", "cursor", since, "err", streamErr)
		if !binary {
			return streamErr
		}
		if err := enginetel.WriteLiveError(w, since, streamErr); err != nil {
			return fmt.Errorf("%w; write live stream error: %v", streamErr, err)
		}
		flush()
		return nil
	}
	for {
		fetchStart := time.Now()
		next, message, rows, err := fetcher(r.Context(), db, since, batchLimit)
		if elapsed := time.Since(fetchStart); elapsed > slowTelemetryOp {
			logger.Warn("slow OTLP stream fetch", "duration", elapsed, "rows", rows, "limit", batchLimit, "error", err)
		}
		if err != nil {
			if r.Context().Err() != nil {
				return nil
			}
			return failStream(fmt.Errorf("fetch: %w", err))
		}
		if rows == 0 {
			if terminating {
				if binary {
					if err := enginetel.WriteLiveTerminal(w, since); err != nil {
						return fmt.Errorf("write terminal frame: %w", err)
					}
					flush()
				}
				return nil
			}
			select {
			case <-time.After(telemetry.NearlyImmediate):
				// Poll at the telemetry batching frequency. Tail reads are cheap,
				// while coupling readers to writers risks blocking shutdown.
			case <-record.shutdownCh:
				logger.ExtraDebug("shutting down")
				terminating = true
			case <-r.Context().Done():
				logger.ExtraDebug("client went away")
				return nil
			}
			continue
		}
		if rows < 0 || rows > batchLimit {
			return failStream(fmt.Errorf("fetch returned invalid row count %d for limit %d", rows, batchLimit))
		}
		if message == nil {
			return failStream(fmt.Errorf("fetch returned %d rows without an OTLP batch", rows))
		}
		if next <= since {
			return failStream(fmt.Errorf("fetch returned non-increasing cursor %d after %d", next, since))
		}

		if binary {
			payloadSize := proto.Size(message)
			if payloadSize > maxPayloadSize {
				if rows == 1 {
					return failStream(fmt.Errorf("telemetry row at cursor %d is %d bytes (maximum frame payload %d)", next, payloadSize, maxPayloadSize))
				}
				// Refetch a strictly smaller prefix at the same cursor. The row count,
				// rather than the previous query limit, bounds this to logarithmically
				// many attempts even when the tail contains fewer rows than requested.
				batchLimit = max(1, rows/2)
				continue
			}
		}

		if binary {
			payload, err := proto.Marshal(message)
			if err != nil {
				return failStream(fmt.Errorf("marshal OTLP batch: %w", err))
			}
			if err := enginetel.WriteLiveFrame(w, next, payload); err != nil {
				return fmt.Errorf("write OTLP frame: %w", err)
			}
		} else {
			payload, err := protojson.Marshal(message)
			if err != nil {
				return failStream(fmt.Errorf("marshal OTLP batch: %w", err))
			}
			if err := (sse.Event{
				Name: legacyTelemetryEventName(r.URL.Path),
				ID:   strconv.FormatInt(next, 10),
				Data: payload,
			}).Write(w); err != nil {
				return fmt.Errorf("write SSE event: %w", err)
			}
		}
		since = next
		batchLimit = otlpBatchSize
		flush()
	}
}
