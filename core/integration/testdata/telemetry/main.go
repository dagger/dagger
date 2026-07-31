package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

		eventsF, err := os.OpenFile(eventsFp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
		defer eventsF.Close()

		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
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
