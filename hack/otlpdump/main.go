// otlpdump is a tiny OTLP http/protobuf receiver that dumps every span, log
// record, and metric data point as one JSON line, for offline inspection of
// the telemetry a dagger run emits.
//
// Usage:
//
//	go run ./hack/otlpdump -addr 127.0.0.1:43180 -out /tmp/telemetry.jsonl
//
// Then run dagger with:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:43180 OTEL_EXPORTER_OTLP_TRACES_LIVE=1 dagger ...
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var (
	addr = flag.String("addr", "127.0.0.1:43180", "listen address")
	out  = flag.String("out", "/tmp/telemetry.jsonl", "output JSONL file")
)

var (
	mu sync.Mutex
	w  io.Writer
)

func emit(record map[string]any) {
	mu.Lock()
	defer mu.Unlock()
	enc, err := json.Marshal(record)
	if err != nil {
		log.Printf("marshal: %v", err)
		return
	}
	fmt.Fprintln(w, string(enc))
}

func attrsToMap(kvs []*commonpb.KeyValue) map[string]any {
	m := map[string]any{}
	for _, kv := range kvs {
		m[kv.Key] = anyValue(kv.Value)
	}
	return m
}

func anyValue(v *commonpb.AnyValue) any {
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_BoolValue:
		return val.BoolValue
	case *commonpb.AnyValue_IntValue:
		return val.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return val.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		var arr []any
		for _, item := range val.ArrayValue.Values {
			arr = append(arr, anyValue(item))
		}
		return arr
	case *commonpb.AnyValue_KvlistValue:
		return attrsToMap(val.KvlistValue.Values)
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(val.BytesValue)
	default:
		return nil
	}
}

func readBody(rw http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func handleTraces(rw http.ResponseWriter, r *http.Request) {
	body, ok := readBody(rw, r)
	if !ok {
		return
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, span := range ss.Spans {
				rec := map[string]any{
					"kind":     "span",
					"traceId":  hex.EncodeToString(span.TraceId),
					"spanId":   hex.EncodeToString(span.SpanId),
					"parentId": hex.EncodeToString(span.ParentSpanId),
					"name":     span.Name,
					"startNs":  span.StartTimeUnixNano,
					"endNs":    span.EndTimeUnixNano,
					"attrs":    attrsToMap(span.Attributes),
					"scope":    ss.Scope.GetName(),
					// DroppedLinksCount lets consumers (e.g. the wcprof OTel
					// loader's structural gate) detect silently-evicted links
					// when a span's link count exceeds the SDK LinkCountLimit.
					"droppedLinks": span.DroppedLinksCount,
				}
				if span.Status != nil && span.Status.Code != 0 {
					rec["status"] = span.Status.Code.String()
					rec["statusMsg"] = span.Status.Message
				}
				if len(span.Links) > 0 {
					var links []map[string]any
					for _, l := range span.Links {
						links = append(links, map[string]any{
							"traceId": hex.EncodeToString(l.TraceId),
							"spanId":  hex.EncodeToString(l.SpanId),
							"attrs":   attrsToMap(l.Attributes),
							// DroppedAttributesCount lets consumers detect
							// link attributes evicted by AttributePerLinkCountLimit
							// (e.g. a wait edge losing its wcprof.wait.* timing).
							"droppedAttrs": l.DroppedAttributesCount,
						})
					}
					rec["links"] = links
				}
				if len(span.Events) > 0 {
					var events []map[string]any
					for _, e := range span.Events {
						events = append(events, map[string]any{
							"name":   e.Name,
							"timeNs": e.TimeUnixNano,
							"attrs":  attrsToMap(e.Attributes),
						})
					}
					rec["events"] = events
				}
				emit(rec)
			}
		}
	}
	rw.WriteHeader(http.StatusOK)
}

func handleLogs(rw http.ResponseWriter, r *http.Request) {
	body, ok := readBody(rw, r)
	if !ok {
		return
	}
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				emit(map[string]any{
					"kind":    "log",
					"traceId": hex.EncodeToString(lr.TraceId),
					"spanId":  hex.EncodeToString(lr.SpanId),
					"timeNs":  lr.TimeUnixNano,
					"body":    anyValue(lr.Body),
					"attrs":   attrsToMap(lr.Attributes),
					"scope":   sl.Scope.GetName(),
				})
			}
		}
	}
	rw.WriteHeader(http.StatusOK)
}

func handleMetrics(rw http.ResponseWriter, r *http.Request) {
	body, ok := readBody(rw, r)
	if !ok {
		return
	}
	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				var points []*metricspb.NumberDataPoint
				var typ string
				switch data := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					typ = "gauge"
					points = data.Gauge.DataPoints
				case *metricspb.Metric_Sum:
					typ = "sum"
					points = data.Sum.DataPoints
				default:
					emit(map[string]any{
						"kind":   "metric",
						"name":   m.Name,
						"type":   fmt.Sprintf("%T", m.Data),
						"scope":  sm.Scope.GetName(),
						"note":   "unhandled data type",
						"points": nil,
					})
					continue
				}
				for _, p := range points {
					rec := map[string]any{
						"kind":   "metric",
						"name":   m.Name,
						"type":   typ,
						"timeNs": p.TimeUnixNano,
						"attrs":  attrsToMap(p.Attributes),
						"scope":  sm.Scope.GetName(),
					}
					switch v := p.Value.(type) {
					case *metricspb.NumberDataPoint_AsInt:
						rec["value"] = v.AsInt
					case *metricspb.NumberDataPoint_AsDouble:
						rec["value"] = v.AsDouble
					}
					emit(rec)
				}
			}
		}
	}
	rw.WriteHeader(http.StatusOK)
}

func main() {
	flag.Parse()

	f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	w = f

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", handleTraces)
	mux.HandleFunc("/v1/logs", handleLogs)
	mux.HandleFunc("/v1/metrics", handleMetrics)

	log.Printf("listening on %s, writing to %s", *addr, *out)
	err = http.ListenAndServe(*addr, mux)
	f.Close()
	log.Fatal(err)
}
