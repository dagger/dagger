package telemetry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	"github.com/moby/buildkit/identity"
	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/slog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type Topic struct {
	TraceID  trace.TraceID
	ClientID string
}

func (t Topic) String() string {
	return fmt.Sprintf("Topic{traceID=%s, clientID=%s}", t.TraceID, t.ClientID)
}

type PubSub struct {
	mux         http.Handler
	traceSubs   map[Topic][]sdktrace.SpanExporter
	traceSubsL  sync.Mutex
	logSubs     map[Topic][]sdklog.Exporter
	logSubsL    sync.Mutex
	metricSubs  map[Topic][]sdkmetric.Exporter
	metricSubsL sync.Mutex
	clients     map[string]*activeClient
	clientsL    sync.Mutex

	// updated via span processor
	spanClients map[spanKey]string
	spanParents map[spanKey]trace.SpanID
	spansL      sync.Mutex
}

type spanKey struct {
	TraceID trace.TraceID
	SpanID  trace.SpanID
}

func NewPubSub() *PubSub {
	mux := http.NewServeMux()
	ps := &PubSub{
		mux:        mux,
		traceSubs:  map[Topic][]sdktrace.SpanExporter{},
		logSubs:    map[Topic][]sdklog.Exporter{},
		metricSubs: map[Topic][]sdkmetric.Exporter{},
		clients:    map[string]*activeClient{},

		spanClients: map[spanKey]string{},
		spanParents: map[spanKey]trace.SpanID{},
	}
	mux.HandleFunc("/v1/traces", ps.TracesHandler)
	mux.HandleFunc("/v1/logs", ps.LogsHandler)
	mux.HandleFunc("/v1/metrics", ps.MetricsHandler)
	return ps
}

// Processor returns a span processor that keeps track of client IDs,
// inheriting them from parent spans if needed.
func (ps *PubSub) Processor() sdktrace.SpanProcessor {
	return clientTracker{ps}
}

type clientTracker struct{ *PubSub }

// OnStart sets the client ID on the span, if it is not already present,
// inheriting it from its parent span if needed by keeping track of all span
// clients.
func (p clientTracker) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	p.spansL.Lock()
	defer p.spansL.Unlock()
	clientID := p.extractClient(ctx, span)
	if clientID != "" {
		span.SetAttributes(attribute.String(telemetry.ClientIDAttr, clientID))
	}
	p.trackSpan(span)
}

// OnEnd does nothing. Span client state must be cleaned up when the client
// goes away, not when a span completes.
func (clientTracker) OnEnd(span sdktrace.ReadOnlySpan) {}

// Shutdown does nothing.
func (clientTracker) Shutdown(context.Context) error { return nil }

// ForceFlush does nothing.
func (clientTracker) ForceFlush(context.Context) error { return nil }

func (p clientTracker) extractClient(ctx context.Context, span sdktrace.ReadOnlySpan) string {
	// slog := slog.With("span", span.Name(), "spanID", span.SpanContext().SpanID())

	// slog.ExtraDebug("!!! ClientAnnotator tracking span clients")

	for _, attr := range span.Attributes() {
		if attr.Key == telemetry.ClientIDAttr {
			// slog.ExtraDebug("!!! > found existing client ID", "value", attr.Value.AsString())
			return attr.Value.AsString()
		}
	}

	var clientID string
	metadata, err := ClientMetadataFromContext(ctx)
	if err == nil {
		clientID = metadata.ClientID
		// slog.ExtraDebug("!!! > adding client ID from ctx", "client", clientID)
	} else if span.Parent().IsValid() {
		// NOTE: empty result if not found is OK
		clientID = p.spanClients[spanKey{
			TraceID: span.SpanContext().TraceID(),
			SpanID:  span.Parent().SpanID(),
		}]
		// slog.ExtraDebug("!!! > inheriting client ID from parent", "client", clientID)
	}

	return clientID
}

// clientsFor returns the relevant client IDs for a given span, traversing
// through parents, in random order.
func (ps *PubSub) clientsFor(traceID trace.TraceID, spanID trace.SpanID) []string {
	ps.spansL.Lock()
	defer ps.spansL.Unlock()
	key := spanKey{
		TraceID: traceID,
		SpanID:  spanID,
	}
	// slog := slog.With("traceID", traceID, "spanID", spanID)
	clients := map[string]struct{}{}
	for {
		if client, ok := ps.spanClients[key]; ok {
			// slog.ExtraDebug("!!! ClientsFor found client", "client", client, "keySpan", key.SpanID)
			clients[client] = struct{}{}
		}
		if parent, ok := ps.spanParents[key]; ok && parent.IsValid() {
			key.SpanID = parent
		} else {
			// slog.ExtraDebug("!!! ClientsFor did not find parent")
			break
		}
	}
	var ids []string
	for id := range clients {
		ids = append(ids, id)
	}
	return ids
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

	// slog := slog.With("span", span.Name(), "spanID", span.SpanContext().SpanID(), "parentID", span.Parent().SpanID())

	ps.spanParents[key] = span.Parent().SpanID()

	// slog.ExtraDebug("!!! ClientAnnotator tracking span clients")

	for _, attr := range span.Attributes() {
		if attr.Key == telemetry.ClientIDAttr {
			// slog.ExtraDebug("!!! > trackSpan tracking client ID", "value", attr.Value.AsString())
			ps.spanClients[key] = attr.Value.AsString()
			return
		}
	}

	// slog.ExtraDebug("!!! trackSpan did not find client ID")
}

type ClientMetadata struct {
	// ClientID is unique to each client. The main client's ID is the empty string,
	// any module and/or nested exec client's ID is a unique digest.
	ClientID string `json:"client_id"`
}

const (
	// NB: a bit odd to move this here, but feels less risky than duplicating
	ClientMetadataMetaKey = "x-dagger-client-metadata"
)

func ClientMetadataFromContext(ctx context.Context) (*ClientMetadata, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("failed to get metadata from context")
	}
	clientMetadata := &ClientMetadata{}
	if err := decodeMeta(md, ClientMetadataMetaKey, clientMetadata); err != nil {
		return nil, err
	}
	return clientMetadata, nil
}

func decodeMeta(md metadata.MD, key string, dest interface{}) error {
	vals, ok := md[key]
	if !ok {
		return fmt.Errorf("failed to get %s from metadata", key)
	}
	if len(vals) != 1 {
		return fmt.Errorf("expected exactly one %s value, got %d", key, len(vals))
	}
	jsonPayload, err := base64.StdEncoding.DecodeString(vals[0])
	if err != nil {
		return fmt.Errorf("failed to base64-decode %s: %w", key, err)
	}
	if err := json.Unmarshal(jsonPayload, dest); err != nil {
		return fmt.Errorf("failed to JSON-unmarshal %s: %w", key, err)
	}
	return nil
}

func (ps *PubSub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ps.mux.ServeHTTP(w, r)
}

func (ps *PubSub) TracesHandler(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling request", "err", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := ps.Spans().ExportSpans(r.Context(), telemetry.SpansFromPB(req.ResourceSpans)); err != nil {
		slog.Error("error exporting spans", "err", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) LogsHandler(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling request", "err", err)
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := ps.Logs().Export(r.Context(), telemetry.LogsFromPB(req.ResourceLogs)); err != nil {
		slog.Error("error exporting spans", "err", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) MetricsHandler(rw http.ResponseWriter, r *http.Request) {
	// TODO
}

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
	} else {
		slog.Warn("draining nonexistant client", "client", client, "immediate", immediate)
	}
	ps.clientsL.Unlock()
}

func (ps *PubSub) initClient(id string) *activeClient {
	ps.clientsL.Lock()
	defer ps.clientsL.Unlock()
	if c, ok := ps.clients[id]; ok {
		return c
	}
	c := &activeClient{
		id:    id,
		cond:  sync.NewCond(&sync.Mutex{}),
		spans: map[trace.SpanID]sdktrace.ReadOnlySpan{},
	}
	ps.clients[id] = c
	return c
}

func (ps *PubSub) lookupClient(id string) (*activeClient, bool) {
	ps.clientsL.Lock()
	defer ps.clientsL.Unlock()
	c, ok := ps.clients[id]
	return c, ok
}

func (ps *PubSub) deinitClient(id string) {
	ps.clientsL.Lock()
	delete(ps.clients, id)
	ps.clientsL.Unlock()

	// free up span parent/client state
	ps.spansL.Lock()
	for key, client := range ps.spanClients {
		if client == id {
			delete(ps.spanParents, key)
			delete(ps.spanClients, key)
		}
	}
	ps.spansL.Unlock()
}

type SpansPubSub struct {
	*PubSub
}

func (ps *PubSub) Spans() sdktrace.SpanExporter {
	return SpansPubSub{ps}
}

func (ps SpansPubSub) allSubscribersForTrace(traceID trace.TraceID) []sdktrace.SpanExporter {
	var exps []sdktrace.SpanExporter
	ps.traceSubsL.Lock()
	for t, subs := range ps.traceSubs {
		if t.TraceID == traceID {
			exps = append(exps, subs...)
		}
	}
	ps.traceSubsL.Unlock()
	return exps
}

func (ps SpansPubSub) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	ps.trackSpans(spans)

	export := identity.NewID()

	slog.ExtraDebug("exporting spans to pubsub", "call", export, "spans", len(spans))

	byExporter := map[sdktrace.SpanExporter][]sdktrace.ReadOnlySpan{}
	updated := map[*activeClient]struct{}{}

	for _, s := range spans {
		var subs []sdktrace.SpanExporter

		affectedClients := ps.clientsFor(
			s.SpanContext().TraceID(),
			s.SpanContext().SpanID(),
		)

		slog := slog.With(
			"span", s.Name(),
			"spanID", s.SpanContext().SpanID(),
			"endTime", s.EndTime(),
			"status", s.Status().Code,
		)

		if len(affectedClients) > 0 {
			for _, clientID := range affectedClients {
				client, subscribed := ps.lookupClient(clientID)
				if !subscribed {
					continue
				}

				if strings.HasSuffix(TracesSource_Subscribe_FullMethodName, s.Name()) ||
					strings.HasSuffix(LogsSource_Subscribe_FullMethodName, s.Name()) {
					// HACK: don't get stuck waiting on ourselves
					slog.ExtraDebug("avoiding waiting for ourselves")
				} else {
					if s.EndTime().Before(s.StartTime()) {
						slog.Trace("starting span", "client", client.id)
						client.startSpan(s)
					} else {
						slog.Trace("finishing span", "client", client.id)
						client.finishSpan(s)
					}
				}

				updated[client] = struct{}{}

				subs = append(subs, ps.SpanSubscribers(Topic{
					TraceID:  s.SpanContext().TraceID(),
					ClientID: clientID,
				})...)
			}

			slog.ExtraDebug("publishing span to affected clients", "clients", affectedClients, "subs", len(subs))
		} else {
			// no clients in particular; send to all client subscribers
			subs = ps.allSubscribersForTrace(s.SpanContext().TraceID())
			slog.ExtraDebug("publishing span to all clients", "subs", len(subs))
		}

		for _, exp := range subs {
			byExporter[exp] = append(byExporter[exp], s)
		}
	}

	defer func() {
		// notify anyone waiting to drain
		for client := range updated {
			slog.Trace("broadcasting to client", "client", client.id)
			client.cond.Broadcast()
		}
	}()

	eg := pool.New().WithErrors()

	for exp, spans := range byExporter {
		exp := exp
		spans := spans
		eg.Go(func() error {
			slog.ExtraDebug("exporting spans to subscriber", "spans", len(spans))
			return exp.ExportSpans(ctx, spans)
		})
	}

	return eg.Wait()
}

func (ps *PubSub) SubscribeToSpans(ctx context.Context, topic Topic, exp sdktrace.SpanExporter) error {
	slog.ExtraDebug("subscribing to spans", "topic", topic)
	client := ps.initClient(topic.ClientID)
	defer ps.deinitClient(topic.ClientID)
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

func (ps *PubSub) Logs() sdklog.Exporter {
	return LogsPubSub{ps}
}

type LogsPubSub struct {
	*PubSub
}

func (ps LogsPubSub) Export(ctx context.Context, logs []sdklog.Record) error {
	slog.ExtraDebug("exporting logs to pub/sub", "logs", len(logs))

	byExporter := map[sdklog.Exporter][]sdklog.Record{}

	for _, rec := range logs {
		topics := map[Topic]struct{}{
			{}:                       {},
			{TraceID: rec.TraceID()}: {},
		}

		// Publish to all clients involved, or the full trace if none.
		for _, clientID := range ps.clientsFor(rec.TraceID(), rec.SpanID()) {
			topics[Topic{
				TraceID:  rec.TraceID(),
				ClientID: clientID,
			}] = struct{}{}
		}

		rec.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.ClientIDAttr {
				topics[Topic{
					TraceID:  rec.TraceID(),
					ClientID: kv.Value.AsString(),
				}] = struct{}{}
				return true
			}
			return false
		})

		for topic := range topics {
			for _, exp := range ps.LogSubscribers(topic) {
				byExporter[exp] = append(byExporter[exp], rec)
			}
		}
	}

	eg := pool.New().WithErrors()

	// export to span subscribers
	for exp, logs := range byExporter {
		exp := exp
		logs := logs
		eg.Go(func() error {
			slog.ExtraDebug("exporting logs to subscriber", "logs", len(logs))
			return exp.Export(ctx, logs)
		})
	}

	return eg.Wait()
}

func (ps *PubSub) SubscribeToLogs(ctx context.Context, topic Topic, exp sdklog.Exporter) error {
	slog.ExtraDebug("subscribing to logs", "topic", topic)
	client := ps.initClient(topic.ClientID)
	defer ps.deinitClient(topic.ClientID)
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

// activeClient keeps track of in-flight spans so that we can wait for them
// all to complete, ensuring we don't drop the last few spans, which ruins
// an entire trace.
type activeClient struct {
	id string

	spans map[trace.SpanID]sdktrace.ReadOnlySpan

	draining         bool
	drainImmediately bool
	cond             *sync.Cond
}

func (trace *activeClient) startSpan(span sdktrace.ReadOnlySpan) {
	trace.cond.L.Lock()
	trace.spans[span.SpanContext().SpanID()] = span
	trace.cond.L.Unlock()
}

func (trace *activeClient) finishSpan(span sdktrace.ReadOnlySpan) {
	trace.cond.L.Lock()
	delete(trace.spans, span.SpanContext().SpanID())
	trace.cond.L.Unlock()
}

func (client *activeClient) spanNames() []string {
	var names []string
	for _, span := range client.spans {
		names = append(names, span.Name())
	}
	return names
}

func (client *activeClient) spanIDs() []string {
	var ids []string
	for _, span := range client.spans {
		ids = append(ids, span.SpanContext().SpanID().String())
	}
	return ids
}

func (client *activeClient) wait(ctx context.Context) {
	slog := slog.With("client", client.id)

	go func() {
		// wake up the loop below if ctx context is interrupted
		<-ctx.Done()
		client.cond.Broadcast()
	}()

	client.cond.L.Lock()
	defer client.cond.L.Unlock()

	for !client.draining || len(client.spans) > 0 {
		slog = client.slogAttrs(slog)
		if ctx.Err() != nil {
			slog.ExtraDebug("wait interrupted")
			break
		}
		if client.drainImmediately {
			slog.ExtraDebug("draining immediately")
			break
		}
		if client.draining {
			slog.Debug("waiting for spans")
		}
		client.cond.Wait()
	}

	slog = client.slogAttrs(slog)
	slog.Debug("done waiting", "ctxErr", ctx.Err())
}

func (client *activeClient) slogAttrs(slog *slog.Logger) *slog.Logger {
	return slog.With(
		"draining", client.draining,
		"immediate", client.drainImmediately,
		"activeSpans", len(client.spans),
		"spanNames", client.spanNames(),
		"spanIDs", client.spanIDs(),
	)
}
