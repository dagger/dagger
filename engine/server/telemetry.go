package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"time"

	"dagger.io/dagger/telemetry"
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

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/vito/go-sse/sse"
)

type Topic struct {
	TraceID  trace.TraceID
	ClientID string
}

func (t Topic) String() string {
	return fmt.Sprintf("Topic{traceID=%s, clientID=%s}", t.TraceID, t.ClientID)
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
	client, err := ps.srv.getClient(sessionID, clientID)
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
	slog.Debug("exporting spans to clients", "spans", len(spans), "clients", len(client.parents)+1)

	eg := new(errgroup.Group)
	for _, c := range append([]*daggerClient{client}, client.parents...) {
		eg.Go(func() error {
			if err := ps.Spans(c).ExportSpans(r.Context(), spans); err != nil {
				return fmt.Errorf("export to %s: %w", c.clientID, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		slog.Error("error exporting spans", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) LogsHandler(rw http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	client, err := ps.srv.getClient(sessionID, clientID)
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

	slog.Debug("exporting logs to clients", "clients", len(client.parents)+1)

	eg := new(errgroup.Group)
	for _, c := range append([]*daggerClient{client}, client.parents...) {
		eg.Go(func() error {
			if err := telemetry.ReexportLogsFromPB(r.Context(), ps.Logs(c), &req); err != nil {
				return fmt.Errorf("export to %s: %w", c.clientID, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		slog.Error("error exporting logs", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) MetricsHandler(rw http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	client, err := ps.srv.getClient(sessionID, clientID)
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

	slog.Debug("exporting metrics to clients", "clients", len(client.parents)+1)

	eg := new(errgroup.Group)
	for _, c := range append([]*daggerClient{client}, client.parents...) {
		eg.Go(func() error {
			if err := enginetel.ReexportMetricsFromPB(r.Context(), []sdkmetric.Exporter{ps.Metrics(c)}, &req); err != nil {
				return fmt.Errorf("export to %s: %w", c.clientID, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		slog.Error("error exporting metrics", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

const otlpBatchSize = 1000

func (ps *PubSub) TracesSubscribeHandler(w http.ResponseWriter, r *http.Request, client *daggerClient) error {
	return ps.sseHandler(w, r, client, func(ctx context.Context, db *sql.DB, lastID string) (*sse.Event, bool, error) {
		var since int64
		if lastID != "" {
			_, err := fmt.Sscanf(lastID, "%d", &since)
			if err != nil {
				return nil, false, fmt.Errorf("invalid last ID: %w", err)
			}
		}
		q := clientdb.New(db)
		spans, err := q.SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{
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
func (ps *PubSub) LogsSubscribeHandler(w http.ResponseWriter, r *http.Request, client *daggerClient) error {
	return ps.sseHandler(w, r, client, func(ctx context.Context, db *sql.DB, lastID string) (*sse.Event, bool, error) {
		var since int64
		if lastID != "" {
			_, err := fmt.Sscanf(lastID, "%d", &since)
			if err != nil {
				return nil, false, fmt.Errorf("invalid last ID: %w", err)
			}
		}
		q := clientdb.New(db)
		logs, err := q.SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{
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
func (ps *PubSub) MetricsSubscribeHandler(w http.ResponseWriter, r *http.Request, client *daggerClient) error {
	return ps.sseHandler(w, r, client, func(ctx context.Context, db *sql.DB, lastID string) (*sse.Event, bool, error) {
		var since int64
		if lastID != "" {
			_, err := fmt.Sscanf(lastID, "%d", &since)
			if err != nil {
				return nil, false, fmt.Errorf("invalid last ID: %w", err)
			}
		}
		q := clientdb.New(db)
		metrics, err := q.SelectMetricsSince(ctx, clientdb.SelectMetricsSinceParams{
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

type SpansPubSub struct {
	*PubSub
	client *daggerClient
}

func (ps *PubSub) Spans(client *daggerClient) sdktrace.SpanExporter {
	return SpansPubSub{
		PubSub: ps,
		client: client,
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, span := range spans {
		names[i] = span.Name()
	}
	return names
}

func (ps SpansPubSub) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	slog.ExtraDebug("pubsub exporting spans", "client", ps.client.clientID, "count", len(spans))

	tx, err := ps.client.db.Begin()
	if err != nil {
		return fmt.Errorf("export spans %+v: begin tx: %w", spanNames(spans), err)
	}
	defer tx.Rollback()

	queries := clientdb.New(tx)

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

		_, err = queries.InsertSpan(ctx, clientdb.InsertSpanParams{
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
		if err != nil {
			return fmt.Errorf("insert span: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

func (ps SpansPubSub) ForceFlush(ctx context.Context) error { return nil }
func (ps SpansPubSub) Shutdown(context.Context) error       { return nil }

func (ps *PubSub) Logs(client *daggerClient) sdklog.Exporter {
	return LogsPubSub{
		PubSub: ps,
		client: client,
	}
}

type LogsPubSub struct {
	*PubSub
	client *daggerClient
}

func (ps LogsPubSub) Export(ctx context.Context, logs []sdklog.Record) error {
	slog.ExtraDebug("pubsub exporting logs", "client", ps.client.clientID, "count", len(logs))

	tx, err := ps.client.db.Begin()
	if err != nil {
		return fmt.Errorf("export logs (%d records): begin tx: %w", len(logs), err)
	}
	defer tx.Rollback()

	queries := clientdb.New(tx)

	for _, rec := range logs {
		traceID := rec.TraceID().String()
		spanID := rec.SpanID().String()
		timestamp := rec.Timestamp().UnixNano()
		severity := int64(rec.Severity())

		var body []byte
		if !rec.Body().Empty() {
			body, err = proto.Marshal(telemetry.LogValueToPB(rec.Body()))
			if err != nil {
				slog.Warn("failed to marshal log record body", "error", err)
				continue
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
			slog.Warn("failed to marshal log record attributes", "error", err)
			continue
		}

		scope, err := protojson.Marshal(telemetry.InstrumentationScopeToPB(rec.InstrumentationScope()))
		if err != nil {
			slog.Warn("failed to marshal log record attributes", "error", err)
			continue
		}

		res := rec.Resource()
		resource, err := protojson.Marshal(telemetry.ResourceToPB(res))
		if err != nil {
			slog.Warn("failed to marshal log record attributes", "error", err)
			continue
		}

		_, err = queries.InsertLog(ctx, clientdb.InsertLogParams{
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
			ResourceSchemaUrl:    res.SchemaURL(),
		})
		if err != nil {
			return fmt.Errorf("insert log: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

func (ps LogsPubSub) ForceFlush(ctx context.Context) error { return nil }
func (ps LogsPubSub) Shutdown(context.Context) error       { return nil }

func (ps *PubSub) Metrics(client *daggerClient) sdkmetric.Exporter {
	return MetricsPubSub{
		PubSub: ps,
		client: client,
	}
}

type MetricsPubSub struct {
	*PubSub
	client *daggerClient
}

func (ps MetricsPubSub) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	slog.ExtraDebug("pubsub exporting metrics", "client", ps.client.clientID, "count", len(metrics.ScopeMetrics))
	if len(metrics.ScopeMetrics) == 0 {
		return nil
	}

	tx, err := ps.client.db.Begin()
	if err != nil {
		return fmt.Errorf("export metrics %+v: begin tx: %w", metrics, err)
	}
	defer tx.Rollback()

	queries := clientdb.New(tx)

	pbMetrics, err := telemetry.ResourceMetricsToPB(metrics)
	if err != nil {
		return fmt.Errorf("convert metrics to pb: %w", err)
	}

	metricsPBBytes, err := protojson.Marshal(pbMetrics)
	if err != nil {
		return fmt.Errorf("marshal metrics to pb: %w", err)
	}

	_, err = queries.InsertMetric(ctx, metricsPBBytes)
	if err != nil {
		return fmt.Errorf("insert metrics: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

func (ps MetricsPubSub) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (ps MetricsPubSub) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (ps MetricsPubSub) ForceFlush(ctx context.Context) error { return nil }
func (ps MetricsPubSub) Shutdown(context.Context) error       { return nil }

type Fetcher func(ctx context.Context, db *sql.DB, since string) (*sse.Event, bool, error)

func (ps *PubSub) sseHandler(w http.ResponseWriter, r *http.Request, client *daggerClient, fetcher Fetcher) error {
	slog := slog.With("client", client.clientID, "path", r.URL.Path)

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

	db, err := ps.srv.clientDBs.Open(client.clientID)
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
		event, hasData, err := fetcher(r.Context(), db, since)
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
				// SQLite should be able to handle aggressive polling just fine.
				// Synchronizing with writes isn't worth the accompanying risk of hangs.
				//
				// NB: logging here is a bit too crazy
			case <-client.shutdownCh:
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
