// spangen streams a large synthetic trace, with metrics and logs, to the OTLP
// endpoints a Dagger exec is handed (OTEL_EXPORTER_OTLP_{TRACES,LOGS,
// METRICS}_ENDPOINT), parented under the exec's span (TRACEPARENT). The
// engine forwards it to the session's clients like any exec's telemetry, so a
// CLI's frontend ingests it exactly as it does a long session's: the
// memory-cake CLI demos use it to grow a `dagger` CLI's telemetry state.
//
// The trace is the shape of dagql/internal/spanfootprint (the footprint
// benchmarks' generator, a long `dagger agent` session): bounded call trees
// of dagql calls (with dag.call frames, dag.inputs, dag.output, cache and
// module attributes, as BenchmarkSpanAttrRetention enriches them) under a few
// long-lived agent loops, plus execs, LLM messages, tool calls, tests and
// checks. Spans go out batch by batch, each batch starting new spans (live
// snapshots, still running) and ending the previous batch's. Exec and test
// spans log a few lines when they end. Alongside, every -metric-interval,
// one metric export: -gauges int64 gauges for each of -series calls, keyed
// by dag.digest, one exemplar per point.
//
// Every string is built per span, so nothing is shared that the wire
// wouldn't share.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Each flag defaults to the environment variable named after it (SPANS,
// METRIC_INTERVAL, ...), so an exec can configure it without arguments.
var (
	nSpans         = flag.Int("spans", envInt("SPANS", 300000), "spans to emit")
	batchSize      = flag.Int("batch", envInt("BATCH", 512), "spans started per batch")
	batchInterval  = flag.Duration("interval", envDuration("INTERVAL", 150*time.Millisecond), "pause between span batches")
	logLines       = flag.Int("log-lines", envInt("LOG_LINES", 3), "log lines per exec or test span")
	series         = flag.Int("series", envInt("SERIES", 200), "metric series (calls) per export")
	gauges         = flag.Int("gauges", envInt("GAUGES", 8), "gauges per series")
	metricExports  = flag.Int("exports", envInt("EXPORTS", 100), "metric exports (0: none)")
	metricInterval = flag.Duration("metric-interval", envDuration("METRIC_INTERVAL", time.Second), "pause between metric exports")
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

// Attribute keys (github.com/dagger/otel-go, engine/telemetryattrs).
const (
	dagDigest          = "dagger.io/dag.digest"
	dagCall            = "dagger.io/dag.call"
	dagInputs          = "dagger.io/dag.inputs"
	dagOutput          = "dagger.io/dag.output"
	dagCached          = "dagger.io/dag.cached"
	uiInternal         = "dagger.io/ui.internal"
	uiBoundary         = "dagger.io/ui.boundary"
	uiActorEmoji       = "dagger.io/ui.actor.emoji"
	uiMessage          = "dagger.io/ui.message"
	checkName          = "dagger.io/check.name"
	checkPassed        = "dagger.io/check.passed"
	linkPurpose        = "dagger.io/link.purpose"
	llmRole            = "dagger.io/llm.role"
	llmTool            = "dagger.io/llm.tool"
	llmToolArgNames    = "dagger.io/llm.tool.args.names"
	llmToolArgValues   = "dagger.io/llm.tool.args.values"
	llmCallDigest      = "dagger.io/llm.call.digest"
	stdioStream        = "stdio.stream"
	metricsSpan        = "dagger.io/metrics.span"
	metricsTrace       = "dagger.io/metrics.trace"
	moduleRef          = "dagger.io/module.ref"
	moduleFunc         = "dagger.io/module.function.name"
	moduleCallerRef    = "dagger.io/module.caller.ref"
	moduleCallerFunc   = "dagger.io/module.caller.function.name"
	originClient       = "dagger.io/telemetry.origin_client_id"
	agent              = "dagger.io/agent"
	agentID            = "dagger.io/agent.id"
	agentName          = "dagger.io/agent.name"
	agentCallDigest    = "dagger.io/agent.call.digest"
	wcprofOpKind       = "wcprof.op.kind"
	execContentDigest  = "dagger.io/execution.content_preferred_digest"
	dagContentDigest   = "dagger.io/dag.content_preferred_digest"
	cacheContract      = "dagger.io/cache.contract"
	cacheOutcome       = "dagger.io/cache.outcome"
	cacheResultID      = "dagger.io/cache.result.id"
	cacheType          = "dagger.io/cache.type"
	testCaseName       = "test.case.name"
	testSuiteName      = "test.suite.name"
	testCaseResult     = "test.case.result.status"
	longLived          = 9    // the root and the agent loops
	maxTreeSize        = 2048 // bounds a call tree (see newTrace)
	errorOriginPurpose = "error_origin"
)

var gaugeNames = []string{
	"dagger.io/metrics.cpustat.usage",
	"dagger.io/metrics.cpustat.user",
	"dagger.io/metrics.cpustat.system",
	"dagger.io/metrics.memory.current",
	"dagger.io/metrics.memory.peak",
	"dagger.io/metrics.iostat.disk.readbytes",
	"dagger.io/metrics.iostat.disk.writebytes",
	"dagger.io/metrics.netstat.rx.bytes",
	"dagger.io/metrics.netstat.tx.bytes",
	"dagger.io/metrics.iostat.pressure.some.total",
	"dagger.io/metrics.cpustat.pressure.some.total",
	"dagger.io/metrics.cpustat.pressure.full.total",
}

var calls = []struct{ typ, field string }{
	{"Container", "withExec"},
	{"Container", "withDirectory"},
	{"Container", "withMountedCache"},
	{"Directory", "file"},
	{"Directory", "withNewFile"},
	{"Query", "container"},
	{"Query", "host"},
	{"File", "contents"},
	{"Workspace", "directory"},
	{"Env", "withStringInput"},
}

var modules = []string{
	"github.com/dagger/dagger/modules/go@v0.18.2",
	"github.com/dagger/dagger/modules/wolfi@v0.18.2",
	"github.com/shykes/daggerverse/hello@3b7a7e8d2c51f0a4c2b6a1e9d8f7c6b5a4e3d2c1",
	"github.com/dagger/dagger/.dagger@local",
}

// hash is a splitmix64 hash of an index and a salt.
func hash(i, salt int) uint64 {
	z := uint64(i)*0x9e3779b97f4a7c15 + uint64(salt)*0xbf58476d1ce4e5b9
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

type gen struct {
	n        int
	traceID  []byte
	parentID []byte // the exec's span: the trace's root hangs off it
	idBase   uint64
	salt     int
	parents  []int32
	starts   []int64

	client   *http.Client
	traces   string
	logs     string
	metrics  string
	resource *resourcepb.Resource

	posts, failures atomic.Int64
	logsSent        atomic.Int64
	pointsSent      atomic.Int64
}

func newGen(n int) (*gen, error) {
	tp := os.Getenv("TRACEPARENT")
	parts := strings.Split(tp, "-")
	if len(parts) < 3 {
		return nil, fmt.Errorf("TRACEPARENT %q: want 00-<trace>-<span>-<flags>", tp)
	}
	traceID, err := hex.DecodeString(parts[1])
	if err != nil || len(traceID) != 16 {
		return nil, fmt.Errorf("TRACEPARENT %q: bad trace ID", tp)
	}
	parentID, err := hex.DecodeString(parts[2])
	if err != nil || len(parentID) != 8 {
		return nil, fmt.Errorf("TRACEPARENT %q: bad span ID", tp)
	}
	endpoint := func(signal string) (string, error) {
		if v := os.Getenv("OTEL_EXPORTER_OTLP_" + signal + "_ENDPOINT"); v != "" {
			return v, nil
		}
		if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
			return strings.TrimSuffix(v, "/") + "/v1/" + strings.ToLower(signal), nil
		}
		return "", fmt.Errorf("no OTLP endpoint for %s", signal)
	}
	g := &gen{
		n:        n,
		traceID:  traceID,
		parentID: parentID,
		idBase:   uint64(time.Now().UnixNano()) << 20,
		salt:     int(time.Now().UnixNano() % 1_000_003),
		parents:  make([]int32, n),
		starts:   make([]int64, n),
		client:   &http.Client{Timeout: 2 * time.Minute},
		resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{str("service.name", "spangen")}},
	}
	for _, s := range []struct {
		dst    *string
		signal string
	}{{&g.traces, "TRACES"}, {&g.logs, "LOGS"}, {&g.metrics, "METRICS"}} {
		if *s.dst, err = endpoint(s.signal); err != nil {
			return nil, err
		}
	}
	// Call trees, as spanfootprint.New builds them.
	var tree, treeEnd int
	for i := 1; i < n; i++ {
		h := hash(i, 0)
		var p int
		switch r := h % 100; {
		case i < longLived:
			p = 0
		case r < 2:
			p = 0
		case i >= treeEnd || r < 10:
			p = 1 + int(h>>8)%(longLived-1)
			if i >= treeEnd {
				tree, treeEnd = i, i+maxTreeSize/16+int(h>>32)%maxTreeSize
			}
		default:
			p = max(tree, i-1-int(h>>8)%48)
		}
		g.parents[i] = int32(p)
	}
	return g, nil
}

func (g *gen) spanID(i int) []byte {
	id := make([]byte, 8)
	binary.BigEndian.PutUint64(id, g.idBase+uint64(i)+1)
	return id
}

func (g *gen) digest(kind string, i int) string {
	return fmt.Sprintf("xxh3:%016x", hash(i, len(kind)+g.salt))
}

func str(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func boolean(k string, v bool) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}}
}

func integer(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func strs(k string, vs ...string) *commonpb.KeyValue {
	vals := make([]*commonpb.AnyValue, len(vs))
	for i, v := range vs {
		vals[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
	}
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: vals}}}}
}

// callFrame encodes a callpbv1.Call (dagql/call/callpbv1/call.proto) as the
// base64 dag.call attribute: receiver digest, return type, field, a string
// argument and an ID argument, and the call's digest.
func callFrame(receiver, typ, field, path, argDigest, digest string) string {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, receiver)
	var t []byte
	t = protowire.AppendTag(t, 1, protowire.BytesType)
	t = protowire.AppendString(t, typ)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, t)
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	b = protowire.AppendString(b, field)
	arg := func(name string, litField protowire.Number, val string) []byte {
		var lit []byte
		lit = protowire.AppendTag(lit, litField, protowire.BytesType)
		lit = protowire.AppendString(lit, val)
		var a []byte
		a = protowire.AppendTag(a, 1, protowire.BytesType)
		a = protowire.AppendString(a, name)
		a = protowire.AppendTag(a, 2, protowire.BytesType)
		a = protowire.AppendBytes(a, lit)
		return a
	}
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	b = protowire.AppendBytes(b, arg("path", 7, path)) // Literal.string
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	b = protowire.AppendBytes(b, arg("directory", 1, argDigest)) // Literal.callDigest
	b = protowire.AppendTag(b, 9, protowire.BytesType)
	b = protowire.AppendString(b, digest)
	return base64.StdEncoding.EncodeToString(b)
}

// span returns span i as exported when it starts (done=false) or ends.
func (g *gen) span(i int, done bool, now int64) (*tracepb.Span, bool) {
	h := hash(i, 1)
	s := &tracepb.Span{
		TraceId:           g.traceID,
		SpanId:            g.spanID(i),
		StartTimeUnixNano: uint64(g.starts[i]),
		// A running span's end, as the SDK exports a live snapshot.
		EndTimeUnixNano: uint64(time.Time{}.UnixNano()),
		Kind:            tracepb.Span_SPAN_KIND_INTERNAL,
	}
	if i == 0 {
		s.ParentSpanId = g.parentID
	} else {
		s.ParentSpanId = g.spanID(int(g.parents[i]))
	}
	if done {
		s.EndTimeUnixNano = uint64(now)
	}
	logs := false
	attrs := []*commonpb.KeyValue{str(originClient, "pbhbtxrpvpl1sxuvaqm5skyrl")}
	switch kind := h % 100; {
	case i == 0:
		// Named for the workload to find (see fixtures/cli/workload.sh).
		s.Name = "spangen-session"
	case i < longLived:
		s.Name = fmt.Sprintf("agent loop %d", i)
		attrs = append(attrs,
			boolean(agent, true),
			str(agentID, fmt.Sprintf("agent-%d", i)),
			str(agentName, fmt.Sprintf("worker-%d", i)),
			str(agentCallDigest, g.digest("agent", i)),
		)
	case kind < 6:
		s.Name = "LLM response"
		attrs = append(attrs,
			str(llmRole, "assistant"),
			str(uiActorEmoji, "🤖"),
			str(uiMessage, "received"),
			str(llmCallDigest, g.digest("llm", i)),
		)
	case kind < 10:
		s.Name = "read"
		attrs = append(attrs,
			str(llmTool, "read"),
			boolean(uiBoundary, true),
			strs(llmToolArgNames, "filePath"),
			strs(llmToolArgValues, fmt.Sprintf("dagql/file%d.go", i)),
		)
	case kind < 12:
		s.Name = fmt.Sprintf("TestThing/case_%d", i)
		attrs = append(attrs, str(testCaseName, s.Name), str(testSuiteName, "TestThing"))
		if done {
			attrs = append(attrs, str(testCaseResult, "pass"))
		}
		logs = true
	case kind < 13:
		s.Name = fmt.Sprintf("check-%d", i%40)
		attrs = append(attrs, str(checkName, s.Name))
		if done {
			attrs = append(attrs, boolean(checkPassed, true))
		}
	case kind < 25:
		s.Name = fmt.Sprintf("exec go test ./pkg%d", i%300)
		attrs = append(attrs,
			str(wcprofOpKind, "exec"),
			str(dagDigest, g.digest("exec", i)),
			str(execContentDigest, g.digest("content", i)),
		)
		logs = true
	default:
		call := calls[h>>8%uint64(len(calls))]
		dig := g.digest("call", i)
		receiver := g.digest("call", int(g.parents[i]))
		arg := fmt.Sprintf("xxh3:%016x", hash(int(h%64), g.salt))
		s.Name = call.typ + "." + call.field
		attrs = append(attrs,
			str(dagDigest, dig),
			str(dagCall, callFrame(receiver, call.typ, call.field, fmt.Sprintf("/src/path/%d", i), arg, dig)),
			str(dagContentDigest, g.digest("content", i)),
			strs(dagInputs, receiver, arg),
		)
		if kind < 70 {
			attrs = append(attrs, boolean(uiInternal, true))
		}
		if h>>40%5 == 0 {
			attrs = append(attrs,
				str(moduleRef, modules[int(h>>44)%len(modules)]),
				str(moduleFunc, "Go.build"),
				str(moduleCallerRef, modules[3]),
				str(moduleCallerFunc, "Dagger.check"),
			)
		}
		if done {
			attrs = append(attrs,
				str(dagOutput, dig),
				str(cacheContract, "v1"),
				str(cacheOutcome, "hit"),
				str(cacheResultID, fmt.Sprint(i)),
				str(cacheType, call.typ),
			)
			if h>>16%4 == 0 {
				attrs = append(attrs, boolean(dagCached, true))
			}
		}
	}
	if h>>20%100 < 5 && i > longLived {
		s.Links = append(s.Links, &tracepb.Span_Link{TraceId: g.traceID, SpanId: g.spanID(int(h>>28) % i)})
	}
	if done && h>>24%100 == 0 && i > longLived {
		s.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "exit code 1"}
		s.Links = append(s.Links, &tracepb.Span_Link{
			TraceId:    g.traceID,
			SpanId:     g.spanID(i - 1),
			Attributes: []*commonpb.KeyValue{str(linkPurpose, errorOriginPurpose)},
		})
	}
	s.Attributes = attrs
	return s, logs
}

func (g *gen) post(ctx context.Context, url string, msg proto.Message) {
	body, err := proto.Marshal(msg)
	if err != nil {
		panic(err)
	}
	g.posts.Add(1)
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			panic(err)
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		resp, err := g.client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				return
			}
			err = fmt.Errorf("%s: %s", url, resp.Status)
		}
		if attempt == 4 {
			g.failures.Add(1)
			fmt.Fprintln(os.Stderr, "spangen:", err)
			return
		}
		time.Sleep(time.Second)
	}
}

func (g *gen) exportSpans(ctx context.Context, spans []*tracepb.Span) {
	g.post(ctx, g.traces, &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource:   g.resource,
			ScopeSpans: []*tracepb.ScopeSpans{{Scope: &commonpb.InstrumentationScope{Name: "spangen"}, Spans: spans}},
		}},
	})
}

func (g *gen) exportLogs(ctx context.Context, records []*logspb.LogRecord) {
	if len(records) == 0 {
		return
	}
	g.logsSent.Add(int64(len(records)))
	g.post(ctx, g.logs, &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource:  g.resource,
			ScopeLogs: []*logspb.ScopeLogs{{Scope: &commonpb.InstrumentationScope{Name: "spangen"}, LogRecords: records}},
		}},
	})
}

func (g *gen) logRecords(i int, now int64) []*logspb.LogRecord {
	recs := make([]*logspb.LogRecord, 0, *logLines)
	for k := 0; k < *logLines; k++ {
		recs = append(recs, &logspb.LogRecord{
			TimeUnixNano:         uint64(now),
			ObservedTimeUnixNano: uint64(now),
			Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{
				StringValue: fmt.Sprintf("=== RUN   TestThing/case_%d/step_%d\nok  \tgithub.com/acme/app/pkg%d\t0.%03ds\n", i, k, i%300, hash(i, k)%1000),
			}},
			Attributes: []*commonpb.KeyValue{integer(stdioStream, 1)},
			TraceId:    g.traceID,
			SpanId:     g.spanID(i),
		})
	}
	return recs
}

// runSpans streams the trace: each batch starts the next spans and ends the
// previous batch's; the root and agent loops end last.
func (g *gen) runSpans(ctx context.Context) {
	b := *batchSize
	for lo := 0; lo < g.n+b; lo += b {
		now := time.Now().UnixNano()
		spans := make([]*tracepb.Span, 0, 2*b)
		var logs []*logspb.LogRecord
		for i := lo; i < min(lo+b, g.n); i++ {
			g.starts[i] = now
			s, _ := g.span(i, false, now)
			spans = append(spans, s)
		}
		for i := max(lo-b, longLived); i < min(lo, g.n); i++ {
			s, hasLogs := g.span(i, true, now)
			spans = append(spans, s)
			if hasLogs {
				logs = append(logs, g.logRecords(i, now)...)
			}
		}
		if len(spans) > 0 {
			g.exportSpans(ctx, spans)
			g.exportLogs(ctx, logs)
		}
		if lo/b%100 == 0 {
			fmt.Printf("spans: %d/%d\n", min(lo+b, g.n), g.n)
		}
		time.Sleep(*batchInterval)
	}
}

// finish ends the root and the agent loops, the root last: a reader that
// sees the root finished has seen everything else.
func (g *gen) finish(ctx context.Context) {
	now := time.Now().UnixNano()
	final := make([]*tracepb.Span, 0, longLived)
	for i := min(longLived, g.n) - 1; i >= 0; i-- {
		s, _ := g.span(i, true, now)
		final = append(final, s)
	}
	g.exportSpans(ctx, final)
}

// runMetrics exports -exports rounds of -gauges gauges for -series calls, one
// exemplar per point.
func (g *gen) runMetrics(ctx context.Context) {
	names := gaugeNames[:min(*gauges, len(gaugeNames))]
	traceHex := hex.EncodeToString(g.traceID)
	start := uint64(time.Now().UnixNano())
	for e := 0; e < *metricExports; e++ {
		now := uint64(time.Now().UnixNano())
		metrics := make([]*metricspb.Metric, 0, len(names))
		for m, name := range names {
			points := make([]*metricspb.NumberDataPoint, 0, *series)
			for s := 0; s < *series; s++ {
				// Series are the execs of the trace's first call trees.
				span := g.spanID(longLived + s*7)
				v := int64(hash(s*131+m, e) % 1_000_000_000)
				points = append(points, &metricspb.NumberDataPoint{
					Attributes: []*commonpb.KeyValue{
						str(dagDigest, g.digest("exec", longLived+s*7)),
						str(metricsSpan, hex.EncodeToString(span)),
						str(metricsTrace, traceHex),
					},
					StartTimeUnixNano: start,
					TimeUnixNano:      now,
					Value:             &metricspb.NumberDataPoint_AsInt{AsInt: v},
					Exemplars: []*metricspb.Exemplar{{
						TimeUnixNano: now,
						Value:        &metricspb.Exemplar_AsInt{AsInt: v},
						SpanId:       span,
						TraceId:      g.traceID,
						FilteredAttributes: []*commonpb.KeyValue{
							str("exemplar.sample", fmt.Sprintf("sample-%d-%d", s, e)),
						},
					}},
				})
			}
			metrics = append(metrics, &metricspb.Metric{
				Name: name,
				Unit: "By",
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}},
			})
		}
		g.pointsSent.Add(int64(len(names) * *series))
		g.post(ctx, g.metrics, &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{{
				Resource:     g.resource,
				ScopeMetrics: []*metricspb.ScopeMetrics{{Scope: &commonpb.InstrumentationScope{Name: "spangen"}, Metrics: metrics}},
			}},
		})
		if e%10 == 0 {
			fmt.Printf("metric exports: %d/%d\n", e+1, *metricExports)
		}
		time.Sleep(*metricInterval)
	}
}

func main() {
	flag.Parse()
	g, err := newGen(*nSpans)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spangen:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	began := time.Now()
	var wg sync.WaitGroup
	if *nSpans > 0 {
		wg.Go(func() { g.runSpans(ctx) })
	}
	if *metricExports > 0 {
		wg.Go(func() { g.runMetrics(ctx) })
	}
	wg.Wait()
	if *nSpans > 0 {
		g.finish(ctx)
	}
	fmt.Printf("spangen: %d spans, %d log records, %d metric points in %d posts (%d failed) over %s\n",
		*nSpans, g.logsSent.Load(), g.pointsSent.Load(), g.posts.Load(), g.failures.Load(), time.Since(began).Round(time.Second))
	if g.failures.Load() > g.posts.Load()/100 {
		os.Exit(1)
	}
}
