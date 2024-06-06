package telemetry

import (
	"context"
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
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
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
	spanDone    map[spanKey]bool
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
		spanDone:    map[spanKey]bool{},
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
func (p clientTracker) OnEnd(span sdktrace.ReadOnlySpan) {
	p.spansL.Lock()
	p.spanDone[spanKey{
		TraceID: span.SpanContext().TraceID(),
		SpanID:  span.SpanContext().SpanID(),
	}] = true
	p.spansL.Unlock()
}

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
	metadata, err := engine.ClientMetadataFromContext(ctx)
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

func (ps *PubSub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ps.mux.ServeHTTP(w, r)
}

func (ps *PubSub) TracesHandler(rw http.ResponseWriter, r *http.Request) {
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

	if err := ps.Spans().ExportSpans(r.Context(), telemetry.SpansFromPB(req.ResourceSpans)); err != nil {
		slog.Error("error exporting spans", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusCreated)
}

func (ps *PubSub) LogsHandler(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("error reading body", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		slog.Error("error unmarshalling request", "err", err)
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	if err := ps.Logs().Export(r.Context(), telemetry.LogsFromPB(req.ResourceLogs)); err != nil {
		slog.Error("error exporting spans", "err", err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
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
	c, ok := ps.clients[id]
	if !ok {
		c = &activeClient{
			ps:         ps,
			id:         id,
			cond:       sync.NewCond(&sync.Mutex{}),
			spans:      map[trace.SpanID]sdktrace.ReadOnlySpan{},
			logStreams: map[logStream]struct{}{},
		}
		ps.clients[id] = c
	}
	c.subscribers++
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
	c, ok := ps.clients[id]
	if ok {
		c.subscribers--
		if c.subscribers == 0 {
			delete(ps.clients, id)
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
		if client == id {
			delete(ps.spanParents, key)
			delete(ps.spanClients, key)
			delete(ps.spanDone, key)
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
			// NOTE: this can happen when a client goes away, but also happens for a
			// few "boring" spans (internal gRPC plumbing etc). because of the first
			// case, we handle this by not emitting it to anyone. at one point we
			// emitted to all clients for the trace, but that led to strange
			// cross-talk with partial data.
			slog.ExtraDebug("no clients interested in span")
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

			client, found := ps.lookupClient(clientID)
			if found {
				client.trackLogStream(rec)
			}
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

type logStream struct {
	span   trace.SpanID
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
	for _, s := range c.spans {
		if s.Parent().SpanID() == span.SpanContext().SpanID() {
			slog.ExtraDebug("abandoning child span",
				"parent", span.Name(),
				"parentID", span.SpanContext().SpanID(),
				"span", s.Name(),
				"spanID", s.SpanContext().SpanID(),
			)
			c.finishAndAbandonChildrenLocked(s)
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
		span:   rec.SpanID(),
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
		// slog.Warn("!!! STREAM EOF", "stream", stream)
		delete(c.logStreams, stream)
		c.cond.Broadcast()
	} else if _, active := c.logStreams[stream]; !active {
		// slog.Warn("!!! STREAM ACTIVE", "stream", stream, "log", rec.Body().AsString())
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
