package main

// Fake Dagger Cloud for integration tests. It records OTLP request bodies
// under /events/<request path>.json, with per-record lines for spans, log
// records and engine cache events, and answers scale-out engine requests.
//
// It doubles as the OAuth token endpoint: a path ending in /oauth/token
// exchanges the expected refresh token for a sequential, short-lived access
// token named after the path prefix, recorded under
// /events/<prefix>/issued-tokens.txt, so tests can tell the engine's
// refreshes from the client's. Telemetry requests must authenticate with the
// static engine token ("test", as basic auth) or a token this server issued;
// anything else is refused, so a stale token cannot deliver telemetry. Every
// authorized request is logged to /events/requests.log as
// "<credential> <path>", and the writer and service of each resource in a
// metrics request to /events/<request path>.json.writers.
//
// A HEAD request, the engine's check that it reaches the Cloud URL, gets a
// bare 401 as from Dagger Cloud, on any path and unrecorded.
//
// Paths starting with /hang/ simulate a Cloud outage: the server reads the
// request and then sits on it longer than any client or engine timeout.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint: gosec
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/hang/") {
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-time.After(60 * time.Second):
			case <-r.Context().Done():
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			issueToken(w, r)
			return
		}
		credential, ok := authorized(r)
		if !ok {
			panic("invalid authorization header: " + r.Header.Get("Authorization"))
		}
		appendFile("/events/requests.log", strings.NewReader(credential+" "+r.URL.Path+"\n"))

		if strings.HasSuffix(r.URL.Path, "/v1/engines") {
			// Scale-out: answer an engine request with the engine the test
			// set up, as Dagger Cloud does.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, os.Getenv("FAKE_CLOUD_ENGINE_SPEC"))
			return
		}

		eventsFp := filepath.Join("/events", fmt.Sprintf("%s.json", r.URL.Path))
		if err := os.MkdirAll(filepath.Dir(eventsFp), 0755); err != nil {
			panic(err)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}

		if strings.HasSuffix(r.URL.Path, "/v1/metrics") {
			appendLines(eventsFp+".writers", metricWriterLines(r, body))
		}
		if strings.HasSuffix(r.URL.Path, "/v1/logs") {
			appendLines(eventsFp+".records", logRecordLines(r, body))
			if events := engineEventLines(r, body); len(events) > 0 {
				// Refuse the first request carrying engine cache events once,
				// so a test can show that the exporter's retry delivers it.
				refusedFp := eventsFp + ".engine-events-refused"
				if _, err := os.Stat(refusedFp); os.IsNotExist(err) {
					if err := os.WriteFile(refusedFp, nil, 0644); err != nil {
						panic(err)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				appendLines(eventsFp+".engine-events", events)
			}
		}

		eventsF, err := os.OpenFile(eventsFp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
		defer eventsF.Close()
		if _, err := eventsF.Write(body); err != nil {
			panic(err)
		}

		if strings.HasSuffix(r.URL.Path, "/v1/traces") {
			var req coltracepb.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				panic(err)
			}
			appendLines(eventsFp+".spans", spanLines(r, &req))
			namesF, err := os.OpenFile(eventsFp+".names", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				panic(err)
			}
			defer namesF.Close()
			for _, resourceSpans := range req.ResourceSpans {
				for _, scopeSpans := range resourceSpans.ScopeSpans {
					for _, span := range scopeSpans.Spans {
						fmt.Fprintln(namesF, span.Name)
					}
				}
			}
		}

		w.WriteHeader(http.StatusCreated)
	}))
	if !errors.Is(err, net.ErrClosed) {
		panic(err)
	}
}

// engineEventScope is the instrumentation scope of the engine's cache events.
const engineEventScope = "dagger.io/engine.cache"

// engineEventLine is one engine cache event as the fake cloud received it.
type engineEventLine struct {
	Export       string            `json:"export"`
	Resource     map[string]string `json:"resource"`
	Attrs        map[string]string `json:"attrs"`
	TimeUnixNano uint64            `json:"timeUnixNano"`
	Body         string            `json:"body"`
}

// engineEventLines returns one JSON line per log record of the engine cache
// event scope in an OTLP logs export.
func engineEventLines(r *http.Request, body []byte) []string {
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		panic(err)
	}
	var lines []string
	for _, resourceLogs := range req.ResourceLogs {
		resource := map[string]string{}
		for _, kv := range resourceLogs.GetResource().GetAttributes() {
			resource[kv.Key] = kv.Value.GetStringValue()
		}
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			if scopeLogs.GetScope().GetName() != engineEventScope {
				continue
			}
			for _, rec := range scopeLogs.LogRecords {
				attrs := map[string]string{}
				for _, kv := range rec.Attributes {
					attrs[kv.Key] = kv.Value.GetStringValue()
				}
				lines = append(lines, jsonLine(engineEventLine{
					Export:       r.Header.Get("X-Dagger-Export"),
					Resource:     resource,
					Attrs:        attrs,
					TimeUnixNano: rec.GetTimeUnixNano(),
					Body:         rec.GetBody().GetStringValue(),
				}))
			}
		}
	}
	return lines
}

// exportWriter is the writer ID of an export's X-Dagger-Export header: one
// per exporter set, so the client's exports and the engine's differ.
func exportWriter(r *http.Request) string {
	writer, _, _ := strings.Cut(r.Header.Get("X-Dagger-Export"), "/")
	return writer
}

// spanLine is one exported span as the fake cloud received it. A running
// span may be exported again when it ends. Session and Cache are the engine
// session and cache its resource names. Attrs holds its string attributes, and
// CacheAttrs, with their types, those that report cache state and span counts:
// those of the dagger.io/cache. and wcprof. namespaces.
type spanLine struct {
	Writer     string            `json:"writer"`
	Service    string            `json:"service"`
	Instance   string            `json:"instance"`
	Session    string            `json:"session,omitempty"`
	Cache      string            `json:"cache,omitempty"`
	TraceID    string            `json:"traceID"`
	SpanID     string            `json:"spanID"`
	Name       string            `json:"name"`
	Attrs      map[string]string `json:"attrs,omitempty"`
	CacheAttrs map[string]any    `json:"cacheAttrs,omitempty"`
}

func spanLines(r *http.Request, req *coltracepb.ExportTraceServiceRequest) []string {
	var lines []string
	for _, resourceSpans := range req.ResourceSpans {
		attrs := resourceSpans.GetResource().GetAttributes()
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				lines = append(lines, jsonLine(spanLine{
					Writer:     exportWriter(r),
					Service:    resourceAttr(attrs, "service.name"),
					Instance:   resourceAttr(attrs, "service.instance.id"),
					Session:    resourceAttr(attrs, "dagger.io/engine.session"),
					Cache:      resourceAttr(attrs, "dagger.io/engine.cache"),
					TraceID:    hex.EncodeToString(span.TraceId),
					SpanID:     hex.EncodeToString(span.SpanId),
					Name:       span.Name,
					Attrs:      stringAttrs(span.Attributes),
					CacheAttrs: cacheStateAttrs(span.Attributes),
				}))
			}
		}
	}
	return lines
}

// cacheStateAttrs returns a span's attributes that report cache state or span
// counts, or nil when it has none.
func cacheStateAttrs(kvs []*commonpb.KeyValue) map[string]any {
	var attrs map[string]any
	for _, kv := range kvs {
		if !strings.HasPrefix(kv.Key, "dagger.io/cache.") && !strings.HasPrefix(kv.Key, "wcprof.") {
			continue
		}
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs[kv.Key] = anyValue(kv.Value)
	}
	return attrs
}

func anyValue(v *commonpb.AnyValue) any {
	switch v := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_BoolValue:
		return v.BoolValue
	case *commonpb.AnyValue_IntValue:
		return v.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return v.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		values := []any{}
		for _, elem := range v.ArrayValue.GetValues() {
			values = append(values, anyValue(elem))
		}
		return values
	}
	return nil
}

// metricWriterLine is the writer and service of one resource's metrics in a
// metrics export.
type metricWriterLine struct {
	Writer  string `json:"writer"`
	Service string `json:"service"`
}

func metricWriterLines(r *http.Request, body []byte) []string {
	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		panic(err)
	}
	var lines []string
	for _, resourceMetrics := range req.ResourceMetrics {
		lines = append(lines, jsonLine(metricWriterLine{
			Writer:  exportWriter(r),
			Service: resourceAttr(resourceMetrics.GetResource().GetAttributes(), "service.name"),
		}))
	}
	return lines
}

// logRecordLine is one exported log record, other than an engine cache
// event, as the fake cloud received it.
type logRecordLine struct {
	Writer   string `json:"writer"`
	Service  string `json:"service"`
	Instance string `json:"instance"`
	Scope    string `json:"scope"`
	Body     string `json:"body"`
}

func logRecordLines(r *http.Request, body []byte) []string {
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		panic(err)
	}
	var lines []string
	for _, resourceLogs := range req.ResourceLogs {
		attrs := resourceLogs.GetResource().GetAttributes()
		service := resourceAttr(attrs, "service.name")
		instance := resourceAttr(attrs, "service.instance.id")
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			if scopeLogs.GetScope().GetName() == engineEventScope {
				continue
			}
			for _, rec := range scopeLogs.LogRecords {
				body := rec.GetBody().GetStringValue()
				if b := rec.GetBody().GetBytesValue(); b != nil {
					body = string(b)
				}
				lines = append(lines, jsonLine(logRecordLine{
					Writer:   exportWriter(r),
					Service:  service,
					Instance: instance,
					Scope:    scopeLogs.GetScope().GetName(),
					Body:     body,
				}))
			}
		}
	}
	return lines
}

func stringAttrs(attrs []*commonpb.KeyValue) map[string]string {
	out := map[string]string{}
	for _, kv := range attrs {
		if v, ok := kv.Value.GetValue().(*commonpb.AnyValue_StringValue); ok {
			out[kv.Key] = v.StringValue
		}
	}
	return out
}

func resourceAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

func jsonLine(v any) string {
	line, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(line)
}

func appendLines(fp string, lines []string) {
	if len(lines) == 0 {
		return
	}
	var buf bytes.Buffer
	for _, line := range lines {
		fmt.Fprintln(&buf, line)
	}
	appendFile(fp, &buf)
}

const refreshToken = "test-refresh-token"

var (
	tokensMu sync.Mutex
	issued   = map[string]bool{}
	counts   = map[string]int{}
)

func issueToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != refreshToken {
		http.Error(w, "unexpected token request: "+r.Form.Encode(), http.StatusBadRequest)
		return
	}
	prefix := strings.TrimSuffix(r.URL.Path, "/oauth/token")

	tokensMu.Lock()
	counts[prefix]++
	token := fmt.Sprintf("fresh-token%s-%d", strings.ReplaceAll(prefix, "/", "-"), counts[prefix])
	issued[token] = true
	tokensMu.Unlock()

	appendFile(filepath.Join("/events", prefix, "issued-tokens.txt"), strings.NewReader(token+"\n"))
	w.Header().Set("Content-Type", "application/json")
	// A 1s expiry keeps the token within oauth2's expiry delta, so every
	// export refreshes again.
	fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","refresh_token":%q,"expires_in":1}`, token, refreshToken)
}

// authorized reports whether the request carries a known credential, and
// returns it so the request log can name it.
func authorized(r *http.Request) (string, bool) {
	if user, _, ok := r.BasicAuth(); ok && user == "test" {
		return "basic:test", true
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	tokensMu.Lock()
	defer tokensMu.Unlock()
	return token, issued[token]
}

func appendFile(fp string, contents io.Reader) {
	if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
		panic(err)
	}
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, contents); err != nil {
		panic(err)
	}
}
