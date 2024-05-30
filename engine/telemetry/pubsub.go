package telemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	spanSubs    map[trace.TraceID][]sdktrace.SpanExporter
	spanSubsL   sync.Mutex
	logSubs     map[trace.TraceID][]sdklog.Exporter
	logSubsL    sync.Mutex
	metricSubs  map[trace.TraceID][]sdkmetric.Exporter
	metricSubsL sync.Mutex
	clients     map[string]*activeClient
	clientsL    sync.Mutex
}

func NewPubSub() *PubSub {
	mux := http.NewServeMux()
	ps := &PubSub{
		mux:        mux,
		spanSubs:   map[trace.TraceID][]sdktrace.SpanExporter{},
		logSubs:    map[trace.TraceID][]sdklog.Exporter{},
		metricSubs: map[trace.TraceID][]sdkmetric.Exporter{},
		clients:    map[string]*activeClient{},
	}
	mux.HandleFunc("/v1/traces", ps.TracesHandler)
	mux.HandleFunc("/v1/logs", ps.LogsHandler)
	mux.HandleFunc("/v1/metrics", ps.MetricsHandler)
	return ps
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
	slog.ExtraDebug("draining", "client", client, "immediate", immediate)
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

func (ps *PubSub) initClient(client string) *activeClient {
	ps.clientsL.Lock()
	defer ps.clientsL.Unlock()
	if t, ok := ps.clients[client]; ok {
		return t
	}
	t := &activeClient{
		cond:     sync.NewCond(&sync.Mutex{}),
		children: map[trace.SpanID]sdktrace.ReadOnlySpan{},
	}
	ps.clients[client] = t
	return t
}

func (ps *PubSub) SubscribeToSpans(ctx context.Context, topic Topic, exp sdktrace.SpanExporter) error {
	slog.ExtraDebug("subscribing to spans", "topic", topic)
	client := ps.initClient(topic.ClientID)
	ps.spanSubsL.Lock()
	ps.spanSubs[topic.TraceID] = append(ps.spanSubs[topic.TraceID], exp)
	ps.spanSubsL.Unlock()
	defer ps.unsubSpans(topic, exp)
	client.wait(ctx)
	return nil
}

type SpansPubSub struct {
	*PubSub
}

func (ps *PubSub) Spans() sdktrace.SpanExporter {
	return SpansPubSub{ps}
}

func getAttr(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func (ps SpansPubSub) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	export := identity.NewID()

	slog.ExtraDebug("exporting spans to pubsub", "call", export, "spans", len(spans))

	byExporter := map[sdktrace.SpanExporter][]sdktrace.ReadOnlySpan{}
	updated := map[Topic]*activeClient{}

	for _, s := range spans {
		topic := Topic{
			TraceID: s.SpanContext().TraceID(),
		}

		if cidVal, ok := getAttr(s.Attributes(), telemetry.ClientIDAttr); ok {
			topic.ClientID = cidVal.AsString()

			client := ps.initClient(topic.ClientID)

			if s.EndTime().Before(s.StartTime()) {
				client.startSpan(s)
			} else {
				client.finishSpan(s)
			}

			updated[topic] = client
		}

		subs := ps.SpanSubscribers(topic)

		slog.ExtraDebug("pubsub exporting span",
			"call", export,
			"topic", topic,
			"subscribers", len(subs),
			"span", s.Name(),
			"spanID", s.SpanContext().SpanID(),
			"status", s.Status().Code,
			"endTime", s.EndTime())

		for _, exp := range subs {
			byExporter[exp] = append(byExporter[exp], s)
		}
	}

	defer func() {
		// notify anyone waiting to drain
		for _, span := range updated {
			span.cond.Broadcast()
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

func (ps *PubSub) SpanSubscribers(topic Topic) []sdktrace.SpanExporter {
	var exps []sdktrace.SpanExporter
	ps.spanSubsL.Lock()
	exps = append(exps, ps.spanSubs[topic.TraceID]...)
	ps.spanSubsL.Unlock()
	return exps
}

func (ps *PubSub) SubscribeToLogs(ctx context.Context, topic Topic, exp sdklog.Exporter) error {
	slog.ExtraDebug("subscribing to logs", "topic", topic)
	client := ps.initClient(topic.ClientID)
	ps.logSubsL.Lock()
	ps.logSubs[topic.TraceID] = append(ps.logSubs[topic.TraceID], exp)
	ps.logSubsL.Unlock()
	defer ps.unsubLogs(topic, exp)
	client.wait(ctx)
	return nil
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
		topic := Topic{
			TraceID: rec.TraceID(),
		}

		rec.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.ClientIDAttr {
				topic.ClientID = kv.Value.AsString()
				return true
			}
			return false
		})

		for _, exp := range ps.LogSubscribers(topic) {
			byExporter[exp] = append(byExporter[exp], rec)
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

func (ps *PubSub) LogSubscribers(topic Topic) []sdklog.Exporter {
	var exps []sdklog.Exporter
	ps.logSubsL.Lock()
	exps = append(exps, ps.logSubs[topic.TraceID]...)
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
	exps = append(exps, ps.metricSubs[topic.TraceID]...)
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
	slog.ExtraDebug("shutting down otel pub/sub")
	ps.spanSubsL.Lock()
	defer ps.spanSubsL.Unlock()
	eg := pool.New().WithErrors()
	for _, ses := range ps.spanSubs {
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
	ps.spanSubsL.Lock()
	removed := make([]sdktrace.SpanExporter, 0, len(ps.spanSubs[topic.TraceID])-1)
	for _, s := range ps.spanSubs[topic.TraceID] {
		if s != exp {
			removed = append(removed, s)
		}
	}
	ps.spanSubs[topic.TraceID] = removed
	ps.spanSubsL.Unlock()
}

func (ps *PubSub) unsubLogs(topic Topic, exp sdklog.Exporter) {
	slog.ExtraDebug("unsubscribing from logs", "topic", topic)
	ps.logSubsL.Lock()
	removed := make([]sdklog.Exporter, 0, len(ps.logSubs[topic.TraceID])-1)
	for _, s := range ps.logSubs[topic.TraceID] {
		if s != exp {
			removed = append(removed, s)
		}
	}
	ps.logSubs[topic.TraceID] = removed
	ps.logSubsL.Unlock()
}

// activeClient keeps track of in-flight spans so that we can wait for them
// all to complete, ensuring we don't drop the last few spans, which ruins
// an entire trace.
type activeClient struct {
	Topic

	children map[trace.SpanID]sdktrace.ReadOnlySpan

	draining         bool
	drainImmediately bool
	cond             *sync.Cond
}

func (trace *activeClient) startSpan(span sdktrace.ReadOnlySpan) {
	trace.cond.L.Lock()
	trace.children[span.SpanContext().SpanID()] = span
	trace.cond.L.Unlock()
}

func (trace *activeClient) finishSpan(span sdktrace.ReadOnlySpan) {
	trace.cond.L.Lock()
	delete(trace.children, span.SpanContext().SpanID())
	trace.cond.L.Unlock()
}

func (trace *activeClient) wait(ctx context.Context) {
	slog := slog.With("topic", trace.Topic)

	go func() {
		// wake up the loop below if ctx context is interrupted
		<-ctx.Done()
		trace.cond.Broadcast()
	}()

	trace.cond.L.Lock()
	for !trace.draining || len(trace.children) > 0 {
		slog = slog.With(
			"draining", trace.draining,
			"immediate", trace.drainImmediately,
			"activeSpans", len(trace.children),
		)
		if ctx.Err() != nil {
			slog.ExtraDebug("wait interrupted")
			break
		}
		if trace.drainImmediately {
			slog.ExtraDebug("draining immediately")
			break
		}
		if trace.draining {
			slog.ExtraDebug("waiting for spans", "activeSpans", len(trace.children))
			for topic, span := range trace.children {
				slog.ExtraDebug("waiting for span", "topic", topic, "span", span.Name())
			}
		}
		trace.cond.Wait()
	}
	slog.ExtraDebug("done waiting", "ctxErr", ctx.Err())
	trace.cond.L.Unlock()
}
