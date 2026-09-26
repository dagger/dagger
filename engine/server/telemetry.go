package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
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
	"github.com/dagger/dagger/engine/agentcontrol"
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
	// Host routing may rebind metadata to an ancestor while the operation
	// still belongs to the scoped client. Identity also survives lease release
	// so final telemetry can be emitted without acquiring execution authority.
	if scope, ok := engine.ClientScopeFromContext(ctx); ok {
		if scope.SessionID() != sessionID {
			return ""
		}
		return scope.ClientID()
	}
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

func cloudEngineTelemetryResource() (*sdkresource.Resource, error) {
	return sdkresource.Merge(
		sdkresource.Default(),
		sdkresource.NewSchemaless(attribute.Bool(telemetryattrs.CloudEngineAttr, true)),
	)
}

// sessionTracerResource is the resource of a session's spans: the SDK default
// plus the engine instance, and the Cloud engine marker on Cloud engines.
func sessionTracerResource(engineInstanceID string, cloudEngine bool) (*sdkresource.Resource, error) {
	base := sdkresource.Default()
	if cloudEngine {
		var err error
		base, err = cloudEngineTelemetryResource()
		if err != nil {
			return nil, err
		}
	}
	return withEngineInstanceResource(base, engineInstanceID)
}

// withEngineInstanceResource adds the engine instance attribute to base.
func withEngineInstanceResource(base *sdkresource.Resource, engineInstanceID string) (*sdkresource.Resource, error) {
	if engineInstanceID == "" {
		return base, nil
	}
	return sdkresource.Merge(base, sdkresource.NewSchemaless(attribute.String(cachefact.ResourceEngineInstance, engineInstanceID)))
}

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
// returns the payload's recipe digest. Any record whose content type declares
// an encoded call belongs to this channel, even when malformed, so it can never
// fall through as an ordinary log record.
//
// The digest is read from the attribute the producer stamps alongside the
// body (telemetryattrs.CallPayloadDigestAttr) precisely so this hot path does
// not decode every body; the body is only decoded for records that lack it.
func classifyCallPayloadRecord(rec sdklog.Record) (digest string, payload bool, err error) {
	claimed := false
	rec.WalkAttributes(func(attr log.KeyValue) bool {
		switch attr.Key {
		case telemetry.ContentTypeAttr:
			claimed = attr.Value.Kind() == log.KindString &&
				attr.Value.AsString() == telemetryattrs.CallPayloadContentType
		case telemetryattrs.CallPayloadDigestAttr:
			if attr.Value.Kind() == log.KindString {
				digest = attr.Value.AsString()
			}
		}
		return true
	})
	if !claimed {
		return "", false, nil
	}
	if rec.Body().Kind() != log.KindBytes {
		return "", true, fmt.Errorf("body must be bytes, got %s", rec.Body().Kind())
	}
	if digest != "" {
		return digest, true, nil
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

// routedLogRecord is one record with its delivery route resolved and its
// origin stamp stripped, ready to fan out.
type routedLogRecord struct {
	rec    sdklog.Record
	route  []string
	digest string // non-empty for call payload records
}

// Export fans records out to the per-client DBs on each record's route.
//
// Call payloads are the exception to plain fan-out: each (digest, target) is
// written at most once per session. The exporter takes exclusive ownership of
// the targets that still need a digest under the session's callPayloadMu —
// atomic per digest, never held across I/O, so exports from the payload
// processor, the ordinary processor and nested clients' /v1/logs POSTs all
// proceed in parallel — writes, then settles: delivered on success, released
// on failure so the payload processor's retry (or a later closure walk) can
// fill the gap without ever duplicating a row that did land.
func (exp sessionLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	// A record that cannot be classified or routed is skipped, not fatal: a
	// missing or unknown origin never resolves on retry, and failing the whole
	// batch would have the payload processor retry it for seconds and then
	// drop its routable siblings too — with their producer claims still held,
	// so no later walk could re-emit them either.
	routed := make([]routedLogRecord, 0, len(records))
	var title *pendingArchiveTitle // the batch's latest main-client title
	for _, rec := range records {
		digest, payload, err := classifyCallPayloadRecord(rec)
		if err != nil {
			slog.Warn("dropping malformed call payload record", "err", err)
			continue
		}
		origin := logOriginClientID(rec)
		if origin == "" {
			if agentcontrol.IsRecord(rec) {
				return fmt.Errorf("protected control missing origin client")
			}
			slog.Warn("dropping log record without telemetry origin client ID", "payload", payload, "digest", digest)
			continue
		}
		route, err := exp.sess.telemetryRouteOriginClientID(origin)
		if err != nil {
			if agentcontrol.IsRecord(rec) {
				return fmt.Errorf("protected control route: %w", err)
			}
			slog.Warn("dropping unroutable log record", "origin", origin, "payload", payload, "digest", digest, "err", err)
			continue
		}
		if agentcontrol.IsRecord(rec) {
			a, edge, err := agentcontrol.Decode(rec)
			if err != nil {
				return fmt.Errorf("decode protected control: %w", err)
			}
			var ns agentcontrol.Namespace
			var projection any
			if a != nil {
				ns, projection = a.Namespace, a
			} else {
				ns, projection = edge.Namespace, edge
			}
			if ns.Session != exp.sess.sessionID || ns.Trace != rec.TraceID().String() {
				return fmt.Errorf("control namespace does not match emission session/trace")
			}
			if err := exp.sess.ensureArchive(ns.Trace); err != nil {
				// Archive availability is not authority to suppress the live roster.
				// The registration failure is retained separately for finalization.
				slog.Warn("register agent archive", "err", err)
			}
			encoded, err := json.Marshal(projection)
			if err != nil {
				return err
			}
			digest = fmt.Sprintf("control:%x", sha256.Sum256(encoded))
			payload = true // reuse post-persistence per-target settlement, in a disjoint key space
		}
		if !payload {
			digest = ""
		}
		if origin == exp.sess.mainClientCallerID && isSpanNameRecord(rec) && rec.Body().Kind() == log.KindString {
			// Only the main client names the session; see setArchiveTitle.
			title = &pendingArchiveTitle{traceID: rec.TraceID().String(), title: rec.Body().AsString()}
		}
		routed = append(routed, routedLogRecord{rec: withoutLogOrigin(rec), route: route, digest: digest})
	}

	byTarget := map[string][]sdklog.Record{}
	payloadsByTarget := map[string][]string{}
	for _, r := range routed {
		route := r.route
		if r.digest != "" {
			// Taking is also the in-batch dedupe: a second copy of the same
			// digest finds its targets already owned by the first.
			route = exp.sess.takeCallPayloadForWrite(r.digest, route)
		}
		for _, target := range route {
			byTarget[target] = append(byTarget[target], r.rec)
			if r.digest != "" {
				payloadsByTarget[target] = append(payloadsByTarget[target], r.digest)
			}
		}
	}

	var eg errgroup.Group
	for target, targetRecords := range byTarget {
		eg.Go(func() error {
			err := exp.ps.Logs(target).Export(ctx, targetRecords)
			for _, digest := range payloadsByTarget[target] {
				exp.sess.settleCallPayload(digest, []string{target}, err == nil)
			}
			if err != nil {
				return fmt.Errorf("export logs to %s: %w", target, err)
			}
			return nil
		})
	}
	err := eg.Wait()
	if title != nil {
		// Titles are advisory and independent of the store writes above.
		exp.sess.setArchiveTitle(title.traceID, title.title)
	}
	return err
}

// isSpanNameRecord reports whether a record renames its span, which is how a
// session publishes its title (telemetryattrs.LogRoleSpanName).
func isSpanNameRecord(rec sdklog.Record) bool {
	found := false
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key != telemetryattrs.LogRoleAttr {
			return true
		}
		found = kv.Value.Kind() == log.KindString && kv.Value.AsString() == telemetryattrs.LogRoleSpanName
		return false
	})
	return found
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
		if agentcontrol.IsRecord(records[i]) {
			return fmt.Errorf("agent control records may only be emitted by engine runtimes")
		}
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
	exporter := record.daggerSession.postedSpanExporter(clientID)
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
	exporter := record.daggerSession.postedLogExporter(clientID)
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
	exporters := record.daggerSession.postedMetricExporters(clientMetricExporter{record: record, ps: ps})
	if err := enginetel.ReexportMetricsFromPB(r.Context(), exporters, &req); err != nil {
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
	if appendErr == nil {
		for _, rec := range logs {
			if agentcontrol.IsRecord(rec) || enginetel.IsCallPayloadRecord(rec) {
				appendErr = db.CheckpointLogs(ctx)
				break
			}
		}
	}
	logTelemetryWrite(ps.clientID, "logs", len(inserts), start, appendStart, stats, appendErr)
	return appendErr
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

//nolint:gocyclo // Keep framing, cursor advancement, and drain handling in one stream state machine.
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
			// A malformed cursor never resolves on retry; a 400 tells the
			// client to give up rather than reconnect every second.
			return httpErr(fmt.Errorf("invalid telemetry cursor %q", cursor), http.StatusBadRequest)
		}
	}

	db, err := record.TelemetryDB(r.Context())
	if err != nil {
		return fmt.Errorf("open client db: %w", err)
	}
	defer db.Close()

	w.Header().Set("Cache-Control", "no-cache")
	if sess := record.daggerSession; sess != nil && sess.publishesToCloud() {
		w.Header().Set(engine.CloudTelemetryPublisherHeader, engine.CloudTelemetryPublisherEngine)
	}
	if binary {
		w.Header().Set("Content-Type", enginetel.LiveContentType)
	} else {
		w.Header().Set("Content-Type", enginetel.LegacyLiveContentType)
		w.Header().Set("Connection", "keep-alive")
	}
	w.WriteHeader(http.StatusOK)
	// Commit and flush the response before waiting for the first batch so the
	// client can distinguish an attached subscription from pending headers.
	// Both encodings write body bytes rather than relying on a header-only
	// flush, which intermediaries (older CLI proxies, buffering HTTP proxies)
	// may hold back until the first body write.
	if binary {
		if err := enginetel.WriteLiveHello(w, since); err != nil {
			return fmt.Errorf("write hello frame: %w", err)
		}
	} else {
		if err := (sse.Event{Name: "subscribed"}).Write(w); err != nil {
			return fmt.Errorf("write subscribed event: %w", err)
		}
	}
	flush()

	terminating := false
	batchLimit := otlpBatchSize
	// failStream ends the stream for good: the client must not reconnect at
	// this cursor. Reserved for protocol violations the next attempt would
	// only repeat (an inconsistent fetcher, an unmarshalable batch).
	failStream := func(streamErr error) error {
		logger.Error("terminating OTLP stream", "cursor", since, "err", streamErr)
		if !binary {
			return streamErr
		}
		if err := enginetel.WriteLiveError(w, since, streamErr); err != nil {
			return fmt.Errorf("%w; write live stream error: %w", streamErr, err)
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
			// A fetch failure is transient trouble (a busy store, an I/O
			// hiccup) and the cursor is still valid, so end the response
			// without a terminal frame: the client sees a lost connection and
			// reconnects at its cursor. Return nil rather than the error — the
			// headers are already committed, so an error would have
			// httpHandlerFunc write an HTTP error body into the stream, which
			// the client would decode as a bad frame and treat as permanent.
			logger.Warn("interrupting OTLP stream", "cursor", since, "err", err)
			return nil
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
					// The row stays in the DB, so ending the stream here would
					// strand the client behind it on every reconnect. Skip it
					// instead: an empty frame carries the client past the row so
					// its cursor stays in step with ours (a reconnect resumes
					// after it, and the terminal frame's cursor still matches).
					logger.Warn("skipping oversized telemetry row", "cursor", next, "bytes", payloadSize, "maxBytes", maxPayloadSize)
					if err := enginetel.WriteLiveFrame(w, next, nil); err != nil {
						return fmt.Errorf("write OTLP skip frame: %w", err)
					}
					since = next
					batchLimit = otlpBatchSize
					flush()
					continue
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
