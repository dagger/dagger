package main

import (
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

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint: gosec
		auth, _, ok := r.BasicAuth()
		if !ok || auth != "test" {
			panic("invalid authorization header")
		}

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

		if strings.HasSuffix(r.URL.Path, "/v1/logs") {
			appendLines(eventsFp+".records", logRecordLines(r, body))
			facts := cacheFactLines(r, body)
			if len(facts) > 0 {
				// Refuse the first request carrying cache facts once, so a test
				// can show that the exporter's retry delivers it.
				failedFp := eventsFp + ".facts-refused"
				if _, err := os.Stat(failedFp); os.IsNotExist(err) {
					if err := os.WriteFile(failedFp, nil, 0644); err != nil {
						panic(err)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				factsF, err := os.OpenFile(eventsFp+".facts", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					panic(err)
				}
				defer factsF.Close()
				for _, line := range facts {
					fmt.Fprintln(factsF, line)
				}
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

// cacheFactLine is one engine cache fact as the fake cloud received it.
type cacheFactLine struct {
	Export   string            `json:"export"`
	Resource map[string]string `json:"resource"`
	Attrs    map[string]string `json:"attrs"`
	Body     string            `json:"body"`
}

// cacheFactLines returns one JSON line per log record of the engine's cache
// fact scope in an OTLP logs export.
func cacheFactLines(r *http.Request, body []byte) []string {
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
			if scopeLogs.GetScope().GetName() != "dagger.io/cache" {
				continue
			}
			for _, rec := range scopeLogs.LogRecords {
				attrs := map[string]string{}
				for _, kv := range rec.Attributes {
					attrs[kv.Key] = kv.Value.GetStringValue()
				}
				line, err := json.Marshal(cacheFactLine{
					Export:   r.Header.Get("X-Dagger-Export"),
					Resource: resource,
					Attrs:    attrs,
					Body:     rec.GetBody().GetStringValue(),
				})
				if err != nil {
					panic(err)
				}
				lines = append(lines, string(line))
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
// span may be exported again when it ends.
type spanLine struct {
	Writer   string `json:"writer"`
	Service  string `json:"service"`
	Instance string `json:"instance"`
	SpanID   string `json:"spanID"`
	Name     string `json:"name"`
}

func spanLines(r *http.Request, req *coltracepb.ExportTraceServiceRequest) []string {
	var lines []string
	for _, resourceSpans := range req.ResourceSpans {
		attrs := resourceSpans.GetResource().GetAttributes()
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				lines = append(lines, jsonLine(spanLine{
					Writer:   exportWriter(r),
					Service:  resourceAttr(attrs, "service.name"),
					Instance: resourceAttr(attrs, "dagger.io/engine.instance"),
					SpanID:   hex.EncodeToString(span.SpanId),
					Name:     span.Name,
				}))
			}
		}
	}
	return lines
}

// logRecordLine is one exported log record, other than a cache fact, as the
// fake cloud received it.
type logRecordLine struct {
	Writer   string `json:"writer"`
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
		instance := resourceAttr(resourceLogs.GetResource().GetAttributes(), "dagger.io/engine.instance")
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			if scopeLogs.GetScope().GetName() == "dagger.io/cache" {
				continue
			}
			for _, rec := range scopeLogs.LogRecords {
				body := rec.GetBody().GetStringValue()
				if b := rec.GetBody().GetBytesValue(); b != nil {
					body = string(b)
				}
				lines = append(lines, jsonLine(logRecordLine{
					Writer:   exportWriter(r),
					Instance: instance,
					Scope:    scopeLogs.GetScope().GetName(),
					Body:     body,
				}))
			}
		}
	}
	return lines
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
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	for _, line := range lines {
		fmt.Fprintln(f, line)
	}
}
