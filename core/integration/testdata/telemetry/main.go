package main

import (
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
	"google.golang.org/protobuf/proto"
)

func main() {
	err := http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint: gosec
		auth, _, ok := r.BasicAuth()
		if !ok || auth != "test" {
			panic("invalid authorization header")
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
