package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"sync"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
)

type Topic struct {
	TraceID  trace.TraceID
	ClientID string
}

func (t Topic) String() string {
	return fmt.Sprintf("Topic{traceID=%s, clientID=%s}", t.TraceID, t.ClientID)
}

type PubSub struct {
	srv        *Server
	mux        http.Handler
	listeners  map[string][]chan<- struct{}
	listenersL sync.Mutex
}

func NewPubSub(srv *Server) *PubSub {
	mux := http.NewServeMux()
	ps := &PubSub{
		srv:       srv,
		mux:       mux,
		listeners: map[string][]chan<- struct{}{},
	}
	mux.HandleFunc("POST /v1/traces", ps.TracesHandler)
	mux.HandleFunc("POST /v1/logs", ps.LogsHandler)
	mux.HandleFunc("POST /v1/metrics", ps.MetricsHandler)
	return ps
}

func (ps *PubSub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ps.mux.ServeHTTP(w, r)
}

func (ps *PubSub) Listen(clientID string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	ps.listenersL.Lock()
	ps.listeners[clientID] = append(ps.listeners[clientID], ch)
	ps.listenersL.Unlock()
	return ch
}

func (ps *PubSub) Notify(clientID string) {
	ps.listenersL.Lock()
	for _, ch := range ps.listeners[clientID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	ps.listenersL.Unlock()
}

func (ps *PubSub) Terminate(clientID string) {
	ps.listenersL.Lock()
	for _, ch := range ps.listeners[clientID] {
		// One last notification to catch any straggling updates.
		select {
		case ch <- struct{}{}:
		default:
		}
		// Close the channel so that for range completes.
		close(ch)
	}
	delete(ps.listeners, clientID)
	ps.listenersL.Unlock()
}

func (ps *PubSub) TracesHandler(rw http.ResponseWriter, r *http.Request) { //nolint: dupl
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	client, err := ps.getClient(sessionID, clientID)
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

	for _, c := range append([]*daggerClient{client}, client.parents...) {
		if err := ps.Spans(c).ExportSpans(r.Context(), telemetry.SpansFromPB(req.ResourceSpans)); err != nil {
			slog.Error("error exporting spans", "err", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) LogsHandler(rw http.ResponseWriter, r *http.Request) { //nolint: dupl
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	client, err := ps.getClient(sessionID, clientID)
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

	for _, c := range append([]*daggerClient{client}, client.parents...) {
		if err := ps.Logs(c).Export(r.Context(), telemetry.LogsFromPB(req.ResourceLogs)); err != nil {
			slog.Error("error exporting logs", "err", err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) TracesSubscribeHandler(w http.ResponseWriter, r *http.Request) { //nolint: dupl
	ps.sseHandler(w, r, func(notify <-chan struct{}, q *clientdb.Queries, emit func(event string, id int64, payload []byte)) {
		var since int64 = 0
		var limit int64 = 1000

		for {
			spans, err := q.SelectSpansSince(r.Context(), clientdb.SelectSpansSinceParams{
				ID:    since,
				Limit: limit,
			})
			if err != nil {
				slog.Error("error selecting spans", "err", err)
				return
			}

			if len(spans) == 0 {
				_, ok := <-notify
				if ok {
					// More data to read.
					continue
				} else {
					// Got 0 spans and the client has terminated, so we're done.
					break
				}
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
				slog.Error("error marshalling spans", "err", err)
				return
			}

			// Send the batch as an OTLP trace export request.
			emit("spans", since, payload)
		}
	})
}

func (ps *PubSub) LogsSubscribeHandler(w http.ResponseWriter, r *http.Request) { //nolint: dupl
	ps.sseHandler(w, r, func(notify <-chan struct{}, q *clientdb.Queries, emit func(event string, id int64, payload []byte)) {
		var since int64 = 0
		var limit int64 = 1000

		for {
			logs, err := q.SelectLogsSince(r.Context(), clientdb.SelectLogsSinceParams{
				ID:    since,
				Limit: limit,
			})
			if err != nil {
				slog.Error("error selecting logs", "err", err)
				return
			}

			if len(logs) == 0 {
				_, ok := <-notify
				if ok {
					// More data to read.
					continue
				} else {
					// Got 0 spans and the client has terminated, so we're done.
					break
				}
			}

			recs := make([]sdklog.Record, len(logs))
			for i, log := range logs {
				recs[i] = log.Record()
				since = log.ID
			}

			// Marshal the spans to OTLP.
			payload, err := protojson.Marshal(&collogspb.ExportLogsServiceRequest{
				ResourceLogs: telemetry.LogsToPB(recs),
			})
			if err != nil {
				slog.Error("error marshalling logs", "err", err)
				return
			}

			// Send the batch as an OTLP trace export request.
			emit("logs", since, payload)
		}
	})
}

func (ps *PubSub) MetricsHandler(rw http.ResponseWriter, r *http.Request) {
	// TODO
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
		attributes, err := clientdb.MarshalProtos(telemetry.KeyValues(span.Attributes()))
		if err != nil {
			slog.Warn("failed to marshal attributes", "error", err)
			continue
		}
		droppedAttributesCount := int64(span.DroppedAttributes())
		events, err := clientdb.MarshalProtos(telemetry.SpanEventsToPB(span.Events()))
		if err != nil {
			slog.Warn("failed to marshal events", "error", err)
			continue
		}
		droppedEventsCount := int64(span.DroppedEvents())
		links, err := clientdb.MarshalProtos(telemetry.SpanLinksToPB(span.Links()))
		if err != nil {
			slog.Warn("failed to marshal links", "error", err)
			continue
		}
		droppedLinksCount := int64(span.DroppedLinks())
		statusCode := int64(span.Status().Code)
		statusMessage := span.Status().Description
		instrumentationScope, err := protojson.Marshal(telemetry.InstrumentationScope(span.InstrumentationScope()))
		if err != nil {
			slog.Warn("failed to marshal instrumentation scope", "error", err)
			continue
		}
		resource, err := protojson.Marshal(telemetry.ResourcePtr(span.Resource()))
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

	ps.Notify(ps.client.clientID)

	return nil
}

// ForceFlush flushes all parents of the client, since we also send to them.
func (ps SpansPubSub) ForceFlush(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, ancestors := range ps.client.parents {
		eg.Go(func() error {
			return ancestors.tp.ForceFlush(ctx)
		})
	}
	return eg.Wait()
}

func (ps SpansPubSub) Shutdown(context.Context) error { return nil }

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

func logValueToJSON(val log.Value) ([]byte, error) {
	return protojson.Marshal(telemetry.LogValueToPB(val))
}

func (ps LogsPubSub) Export(ctx context.Context, logs []sdklog.Record) error {
	slog.ExtraDebug("pubsub exporting logs", "client", ps.client.clientID, "count", len(logs))

	tx, err := ps.client.db.Begin()
	if err != nil {
		return fmt.Errorf("export logs %+v: begin tx: %w", logs, err)
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
			body, err = logValueToJSON(rec.Body())
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
		attributes, err := clientdb.MarshalProtos(attrs)
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
			Timestamp:  timestamp,
			Severity:   severity,
			Body:       body,
			Attributes: attributes,
		})
		if err != nil {
			return fmt.Errorf("insert log: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	ps.Notify(ps.client.clientID)

	return nil
}

// ForceFlush flushes all parents of the client, since we also send to them.
func (ps LogsPubSub) ForceFlush(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, ancestors := range ps.client.parents {
		eg.Go(func() error {
			return ancestors.tp.ForceFlush(ctx)
		})
	}
	return eg.Wait()
}

func (ps LogsPubSub) Shutdown(context.Context) error { return nil }

func (ps *PubSub) sseHandler(w http.ResponseWriter, r *http.Request, handler func(<-chan struct{}, *clientdb.Queries, func(string, int64, []byte))) {
	sessionID := r.Header.Get("X-Dagger-Session-ID")
	clientID := r.Header.Get("X-Dagger-Client-ID")
	client, err := ps.getClient(sessionID, clientID)
	if err != nil {
		slog.Warn("error getting client", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flush := func() {}
	if flusher, ok := w.(http.Flusher); ok {
		flush = flusher.Flush
	}

	// Set CORS headers to allow all origins. You may want to restrict this to specific origins in a production environment.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Type")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	q := clientdb.New(client.db)

	notify := ps.Listen(clientID)

	handler(notify, q, func(event string, id int64, data []byte) {
		// Send the batch as an OTLP trace export request.
		fmt.Fprintf(w, "event: spans\n")
		fmt.Fprintf(w, "id: %d\n", id)
		fmt.Fprintf(w, "data: %s\n", string(data))
		fmt.Fprintln(w)
		flush()
	})
}

func (ps *PubSub) getClient(sessionID, clientID string) (*daggerClient, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("missing session ID")
	}
	if clientID == "" {
		return nil, fmt.Errorf("missing client ID")
	}
	ps.srv.daggerSessionsMu.RLock()
	sess, ok := ps.srv.daggerSessions[sessionID]
	ps.srv.daggerSessionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	sess.clientMu.RLock()
	client, ok := sess.clients[clientID]
	sess.clientMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client %q not found", clientID)
	}
	return client, nil
}
