package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
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
	clientDBs *clientdb.DBs

	mux http.Handler

	listeners     map[string][]chan<- struct{}
	listenersL    sync.Mutex
	traceClients  map[trace.TraceID]map[string]struct{}
	traceClientsL sync.Mutex
	traceSubs     map[Topic][]sdktrace.SpanExporter
	traceSubsL    sync.Mutex
	logSubs       map[Topic][]sdklog.Exporter
	logSubsL      sync.Mutex
	metricSubs    map[Topic][]sdkmetric.Exporter
	metricSubsL   sync.Mutex
	clients       map[string]*activeClient
	clientsL      sync.Mutex

	// updated via span processor
	spanClients map[spanKey]string
	spanParents map[spanKey]trace.SpanID
	spansL      sync.Mutex
}

type spanKey struct {
	TraceID trace.TraceID
	SpanID  trace.SpanID
}

func NewPubSub(clientDBs *clientdb.DBs) *PubSub {
	mux := http.NewServeMux()
	ps := &PubSub{
		mux:          mux,
		clientDBs:    clientDBs,
		listeners:    map[string][]chan<- struct{}{},
		traceClients: map[trace.TraceID]map[string]struct{}{},
		traceSubs:    map[Topic][]sdktrace.SpanExporter{},
		logSubs:      map[Topic][]sdklog.Exporter{},
		metricSubs:   map[Topic][]sdkmetric.Exporter{},
		clients:      map[string]*activeClient{},

		spanClients: map[spanKey]string{},
		spanParents: map[spanKey]trace.SpanID{},
	}
	mux.HandleFunc("POST /v1/traces", ps.TracesHandler)
	mux.HandleFunc("POST /v1/logs", ps.LogsHandler)
	mux.HandleFunc("POST /v1/metrics", ps.MetricsHandler)
	return ps
}

// Processor returns a span processor that keeps track of client IDs,
// inheriting them from parent spans if needed.
func (ps *PubSub) Processor() sdktrace.SpanProcessor {
	return clientTracker{ps}
}

type clientTracker struct{ *PubSub }

// OnStart keeps track of the client ID and parent span ID for each span,
// setting it to the starting context's client ID if not present.
func (p clientTracker) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	p.spansL.Lock()
	defer p.spansL.Unlock()

	// respect existing client ID. not sure if load bearing, just seems logical;
	// better to trust the source.
	for _, attr := range span.Attributes() {
		if attr.Key == telemetry.ClientIDAttr {
			p.trackSpan(span)
			return
		}
	}

	// extract client ID from calling context.
	metadata, err := engine.ClientMetadataFromContext(ctx)
	if err == nil {
		span.SetAttributes(attribute.String(telemetry.ClientIDAttr, metadata.ClientID))
	}

	// track the span, whether we found a client ID or not, so we can step
	// through parents.
	p.trackSpan(span)
}

// OnEnd does nothing. Span state is cleaned up when clients go away, not when
// a span completes.
func (clientTracker) OnEnd(span sdktrace.ReadOnlySpan) {}

// Shutdown does nothing.
func (clientTracker) Shutdown(context.Context) error { return nil }

// ForceFlush does nothing.
func (clientTracker) ForceFlush(context.Context) error { return nil }

// clientsFor returns the relevant client IDs for a given span, traversing
// through parents, in random order.
func (ps *PubSub) clientsFor(traceID trace.TraceID, spanID trace.SpanID) []string {
	ps.spansL.Lock()
	defer ps.spansL.Unlock()
	key := spanKey{
		TraceID: traceID,
		SpanID:  spanID,
	}
	clients := map[string]struct{}{}
	seen := map[spanKey]bool{}
	for {
		if seen[key] {
			// something horrible has happened, better than looping forever
			slog.Error("cycle detected collecting span clients",
				"originalSpanID", spanID,
				"traceID", key.TraceID,
				"spanID", key.SpanID,
				"seen", seen)
			break
		}
		seen[key] = true
		if client, ok := ps.spanClients[key]; ok {
			clients[client] = struct{}{}
		}
		if parent, ok := ps.spanParents[key]; ok && parent.IsValid() {
			key.SpanID = parent
		} else {
			break
		}
	}
	var ids []string
	for id := range clients {
		ids = append(ids, id)
	}
	return ids
}

func (ps *PubSub) clientFor(traceID trace.TraceID, spanID trace.SpanID) string {
	ps.spansL.Lock()
	defer ps.spansL.Unlock()
	return ps.spanClients[spanKey{
		TraceID: traceID,
		SpanID:  spanID,
	}]
}

func (ps *PubSub) trackSpans(spans []sdktrace.ReadOnlySpan) {
	ps.spansL.Lock()
	defer ps.spansL.Unlock()
	for _, span := range spans {
		ps.trackSpan(span)
	}
}

func (ps *PubSub) trackSpan(span sdktrace.ReadOnlySpan) {
	key := spanKey{
		TraceID: span.SpanContext().TraceID(),
		SpanID:  span.SpanContext().SpanID(),
	}

	ps.spanParents[key] = span.Parent().SpanID()

	for _, attr := range span.Attributes() {
		if attr.Key == telemetry.ClientIDAttr {
			ps.spanClients[key] = attr.Value.AsString()
			return
		}
	}
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
	clientID := r.Header.Get("X-Dagger-Client-ID")
	if clientID == "" {
		slog.Warn("missing client ID")
		http.Error(rw, "missing client ID", http.StatusBadRequest)
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
		slog.Error("error unmarshalling request", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	db, err := ps.clientDBs.Open(clientID)
	if err != nil {
		slog.Error("error opening spans exporter", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	if err := ps.Spans(clientID, db).ExportSpans(r.Context(), telemetry.SpansFromPB(req.ResourceSpans)); err != nil {
		slog.Error("error exporting spans", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) sseHandler(w http.ResponseWriter, r *http.Request, handler func(<-chan struct{}, *clientdb.Queries, func(string, int64, []byte))) {
	clientID := r.Header.Get("X-Dagger-Client-ID")
	if clientID == "" {
		slog.Warn("missing client ID")
		http.Error(w, "missing client ID", http.StatusBadRequest)
		return
	}

	db, err := ps.clientDBs.Open(clientID)
	if err != nil {
		slog.Error("error opening spans exporter", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

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

	q := clientdb.New(db)

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

func (ps *PubSub) LogsHandler(rw http.ResponseWriter, r *http.Request) { //nolint: dupl
	clientID := r.Header.Get("X-Dagger-Client-ID")
	if clientID == "" {
		slog.Warn("missing client ID")
		http.Error(rw, "missing client ID", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	var req collogspb.ExportLogsServiceRequest
	if err := protojson.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling request", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	db, err := ps.clientDBs.Open(clientID)
	if err != nil {
		slog.Error("error opening spans exporter", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	if err := ps.Logs(clientID, db).Export(r.Context(), telemetry.LogsFromPB(req.ResourceLogs)); err != nil {
		slog.Error("error exporting spans", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
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

const drainTimeout = 10 * time.Second

func (ps *PubSub) Drain(client string, immediate bool) {
	slog.Debug("draining", "client", client, "immediate", immediate)
	ps.clientsL.Lock()
	trace, ok := ps.clients[client]
	if ok {
		trace.cond.L.Lock()
		trace.draining = true
		trace.drainImmediately = immediate
		trace.cond.Broadcast()
		trace.cond.L.Unlock()
		if !immediate && drainTimeout > 0 {
			go func() {
				<-time.After(drainTimeout)
				trace.cond.L.Lock()
				trace.drainImmediately = true
				trace.cond.Broadcast()
				trace.cond.L.Unlock()
			}()
		}
	} else {
		slog.Warn("draining nonexistant client", "client", client, "immediate", immediate)
	}
	ps.clientsL.Unlock()
}

func (ps *PubSub) initTopic(topic Topic) *activeClient {
	traceID := topic.TraceID
	clientID := topic.ClientID

	ps.traceClientsL.Lock()
	clients, found := ps.traceClients[traceID]
	if !found {
		clients = map[string]struct{}{}
		ps.traceClients[traceID] = clients
	}
	clients[clientID] = struct{}{}
	ps.traceClientsL.Unlock()

	ps.clientsL.Lock()
	c, ok := ps.clients[clientID]
	if !ok {
		c = &activeClient{
			ps:         ps,
			id:         clientID,
			cond:       sync.NewCond(&sync.Mutex{}),
			spans:      map[trace.SpanID]sdktrace.ReadOnlySpan{},
			logStreams: map[logStream]struct{}{},
		}
		ps.clients[clientID] = c
	}
	c.subscribers++
	defer ps.clientsL.Unlock()

	return c
}

func (ps *PubSub) deinitTopic(topic Topic) {
	traceID := topic.TraceID
	clientID := topic.ClientID

	var lastForTrace bool
	ps.traceClientsL.Lock()
	clients, found := ps.traceClients[traceID]
	if !found {
		clients = map[string]struct{}{}
		ps.traceClients[traceID] = clients
	}
	delete(clients, clientID)
	if len(clients) == 0 {
		lastForTrace = true
		delete(ps.traceClients, traceID)
	}
	ps.traceClientsL.Unlock()

	ps.clientsL.Lock()
	c, ok := ps.clients[clientID]
	if ok {
		c.subscribers--
		if c.subscribers == 0 {
			delete(ps.clients, clientID)
		} else {
			// still an active subscriber for this client; keep it around
			ps.clientsL.Unlock()
			return
		}
	}
	ps.clientsL.Unlock()

	// free up span parent/client state
	ps.spansL.Lock()
	for key, client := range ps.spanClients {
		if client == clientID || (lastForTrace && key.TraceID == traceID) {
			delete(ps.spanParents, key)
			delete(ps.spanClients, key)
		}
	}
	ps.spansL.Unlock()
}

func (ps *PubSub) lookupClient(id string) (*activeClient, bool) {
	ps.clientsL.Lock()
	defer ps.clientsL.Unlock()
	c, ok := ps.clients[id]
	return c, ok
}

type SpansPubSub struct {
	*PubSub
	db       *sql.DB
	clientID string
}

func (ps *PubSub) Spans(clientID string, db *sql.DB) sdktrace.SpanExporter {
	return SpansPubSub{
		PubSub:   ps,
		db:       db,
		clientID: clientID,
	}
}

func (ps SpansPubSub) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	tx, err := ps.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
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

	ps.Notify(ps.clientID)

	return nil
}

func (ps *PubSub) SubscribeToSpans(ctx context.Context, topic Topic, exp sdktrace.SpanExporter) error {
	slog.ExtraDebug("subscribing to spans", "topic", topic)
	client := ps.initTopic(topic)
	defer ps.deinitTopic(topic)
	ps.traceSubsL.Lock()
	ps.traceSubs[topic] = append(ps.traceSubs[topic], exp)
	ps.traceSubsL.Unlock()
	defer ps.unsubSpans(topic, exp)
	client.wait(ctx)
	return nil
}

func (ps *PubSub) SpanSubscribers(topic Topic) []sdktrace.SpanExporter {
	var exps []sdktrace.SpanExporter
	ps.traceSubsL.Lock()
	exps = append(exps, ps.traceSubs[topic]...)
	ps.traceSubsL.Unlock()
	return exps
}

func (ps *PubSub) Logs(clientID string, db *sql.DB) sdklog.Exporter {
	return LogsPubSub{
		PubSub:   ps,
		db:       db,
		clientID: clientID,
	}
}

type LogsPubSub struct {
	*PubSub
	db       *sql.DB
	clientID string
}

func logValueToJSON(val log.Value) ([]byte, error) {
	return protojson.Marshal(telemetry.LogValueToPB(val))
}

func logValueFromJSON(val []byte) (log.Value, error) {
	anyVal := &otlpcommonv1.AnyValue{}
	if err := protojson.Unmarshal(val, anyVal); err != nil {
		return log.Value{}, err
	}
	return telemetry.LogValueFromPB(anyVal), nil
}

func (ps LogsPubSub) Export(ctx context.Context, logs []sdklog.Record) error {
	tx, err := ps.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
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

	ps.Notify(ps.clientID)

	return nil
}

func (ps *PubSub) SubscribeToLogs(ctx context.Context, topic Topic, exp sdklog.Exporter) error {
	slog.ExtraDebug("subscribing to logs", "topic", topic)
	client := ps.initTopic(topic)
	defer ps.deinitTopic(topic)
	ps.logSubsL.Lock()
	ps.logSubs[topic] = append(ps.logSubs[topic], exp)
	ps.logSubsL.Unlock()
	defer ps.unsubLogs(topic, exp)
	client.wait(ctx)
	return nil
}

func (ps *PubSub) LogSubscribers(topic Topic) []sdklog.Exporter {
	var exps []sdklog.Exporter
	ps.logSubsL.Lock()
	exps = append(exps, ps.logSubs[topic]...)
	ps.logSubsL.Unlock()
	return exps
}

func (ps *PubSub) Metrics() sdkmetric.Exporter {
	return MetricsPubSub{ps}
}

type MetricsPubSub struct {
	*PubSub
}

func (ps MetricsPubSub) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (ps MetricsPubSub) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (ps MetricsPubSub) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	slog.Warn("TODO: support exporting metrics to pub/sub", "metrics", len(metrics.ScopeMetrics))
	return nil
}

func (ps MetricsPubSub) MetricSubscribers(topic Topic) []sdkmetric.Exporter {
	var exps []sdkmetric.Exporter
	ps.metricSubsL.Lock()
	exps = append(exps, ps.metricSubs[topic]...)
	ps.metricSubsL.Unlock()
	return exps
}

// NB: this is part of the Metrics exporter interface only for some reason, but
// it would be the same signature across the others too anyway.
func (ps *PubSub) ForceFlush(ctx context.Context) error {
	slog.Warn("TODO: forcing flush of metrics")
	return nil
}

func (ps *PubSub) Shutdown(ctx context.Context) error {
	slog.Debug("shutting down otel pub/sub")
	ps.traceSubsL.Lock()
	defer ps.traceSubsL.Unlock()
	eg := pool.New().WithErrors()
	for _, ses := range ps.traceSubs {
		for _, se := range ses {
			se := se
			eg.Go(func() error {
				return se.Shutdown(ctx)
			})
		}
	}
	return eg.Wait()
}

func (ps *PubSub) unsubSpans(topic Topic, exp sdktrace.SpanExporter) {
	slog.ExtraDebug("unsubscribing from trace", "topic", topic)
	ps.traceSubsL.Lock()
	removed := make([]sdktrace.SpanExporter, 0, len(ps.traceSubs[topic])-1)
	for _, s := range ps.traceSubs[topic] {
		if s != exp {
			removed = append(removed, s)
		}
	}
	ps.traceSubs[topic] = removed
	ps.traceSubsL.Unlock()
}

func (ps *PubSub) unsubLogs(topic Topic, exp sdklog.Exporter) {
	slog.ExtraDebug("unsubscribing from logs", "topic", topic)
	ps.logSubsL.Lock()
	removed := make([]sdklog.Exporter, 0, len(ps.logSubs[topic])-1)
	for _, s := range ps.logSubs[topic] {
		if s != exp {
			removed = append(removed, s)
		}
	}
	ps.logSubs[topic] = removed
	ps.logSubsL.Unlock()
}

type logStream struct {
	span   spanKey
	stream int64
}

func (s logStream) String() string {
	return fmt.Sprintf("logStream{span=%s, stream=%d}", s.span, s.stream)
}

// activeClient keeps track of in-flight spans so that we can wait for them
// all to complete, ensuring we don't drop the last few spans, which ruins
// an entire trace.
type activeClient struct {
	ps *PubSub

	// keep track of parallel logs/traces/metrics subscriptions
	subscribers int

	id string

	spans      map[trace.SpanID]sdktrace.ReadOnlySpan
	logStreams map[logStream]struct{}

	draining         bool
	drainImmediately bool
	cond             *sync.Cond
}

func (c *activeClient) startSpan(span sdktrace.ReadOnlySpan) {
	c.cond.L.Lock()
	c.spans[span.SpanContext().SpanID()] = span
	c.cond.L.Unlock()
}

func (c *activeClient) finishSpan(span sdktrace.ReadOnlySpan) {
	c.cond.L.Lock()
	c.finishAndAbandonChildrenLocked(span)
	c.cond.L.Unlock()
}

func (c *activeClient) finishAndAbandonChildrenLocked(span sdktrace.ReadOnlySpan) {
	delete(c.spans, span.SpanContext().SpanID())
	if span.Status().Code == codes.Error {
		for _, s := range c.spans {
			if s.Parent().SpanID() == span.SpanContext().SpanID() {
				slog.ExtraDebug("abandoning child span due to failed parent",
					"parent", span.Name(),
					"parentID", span.SpanContext().SpanID(),
					"span", s.Name(),
					"spanID", s.SpanContext().SpanID(),
				)
				c.finishAndAbandonChildrenLocked(s)
			}
		}
	}
}

func (c *activeClient) spanNames() []string {
	var names []string
	for _, span := range c.spans {
		names = append(names, span.Name())
	}
	return names
}

func (c *activeClient) spanIDs() []string {
	var ids []string
	for _, span := range c.spans {
		ids = append(ids, span.SpanContext().SpanID().String())
	}
	return ids
}

func (c *activeClient) trackLogStream(rec sdklog.Record) {
	stream := logStream{
		span: spanKey{
			TraceID: rec.TraceID(),
			SpanID:  rec.SpanID(),
		},
		stream: -1,
	}
	var eof bool
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		switch kv.Key {
		case telemetry.StdioStreamAttr:
			stream.stream = kv.Value.AsInt64()
		case telemetry.StdioEOFAttr:
			eof = kv.Value.AsBool()
		}
		return true
	})
	if stream.stream == -1 {
		// log record doesn't conform to this stream/EOF pattern, so just ignore it
		return
	}
	c.cond.L.Lock()
	if eof {
		delete(c.logStreams, stream)
		c.cond.Broadcast()
	} else if _, active := c.logStreams[stream]; !active {
		c.logStreams[stream] = struct{}{}
		c.cond.Broadcast()
	}
	c.cond.L.Unlock()
}

func (c *activeClient) wait(ctx context.Context) {
	slog := slog.With("client", c.id)

	go func() {
		// wake up the loop below if ctx context is interrupted
		<-ctx.Done()
		c.cond.Broadcast()
	}()

	c.cond.L.Lock()
	defer c.cond.L.Unlock()

	for !c.draining || len(c.spans) > 0 || len(c.logStreams) > 0 {
		slog = c.slogAttrs(slog)
		if ctx.Err() != nil {
			slog.ExtraDebug("wait interrupted")
			break
		}
		if c.drainImmediately {
			slog.ExtraDebug("draining immediately")
			break
		}
		if c.draining {
			slog.Debug("waiting for spans and/or logs to drain")
		}
		c.cond.Wait()
	}

	slog = c.slogAttrs(slog)
	slog.Debug("done waiting", "ctxErr", ctx.Err())
}

func (c *activeClient) slogAttrs(slog *slog.Logger) *slog.Logger {
	return slog.With(
		"draining", c.draining,
		"immediate", c.drainImmediately,
		"activeSpans", len(c.spans),
		"activeLogs", c.logStreams,
		"spanNames", c.spanNames(),
		"spanIDs", c.spanIDs(),
	)
}
