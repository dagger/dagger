// Package foldreplay measures how much memory a dagui DB holds for a real
// trace, folding (dagui.WithFold) or not, by replaying the trace the way a
// live session streams it. See BenchmarkFoldReplay for how to run it.
//
// A capture is a file of OTLP trace export requests, in either of two
// formats, told apart by the file's extension:
//
//   - .otlp: each request a protobuf ExportTraceServiceRequest, preceded by
//     its length as a uvarint (what Capture, Serve and FetchCloud write);
//   - .jsonl: each request a protojson ExportTraceServiceRequest on a line
//     of its own (what an OpenTelemetry Collector's file exporter writes).
//
// A capture may hold several snapshots of a span; the replay takes its last
// ended one (else its last).
package foldreplay

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

// Capture writes a capture in the .otlp format.
type Capture struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

// Create creates a capture at path.
func Create(path string) (*Capture, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Capture{f: f, w: bufio.NewWriterSize(f, 1<<20)}, nil
}

// Write appends a request.
func (c *Capture) Write(req *coltracepb.ExportTraceServiceRequest) error {
	b, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var n [binary.MaxVarintLen64]byte
	if _, err := c.w.Write(n[:binary.PutUvarint(n[:], uint64(len(b)))]); err != nil {
		return err
	}
	_, err = c.w.Write(b)
	return err
}

// Close flushes and closes the capture.
func (c *Capture) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return errors.Join(c.w.Flush(), c.f.Close())
}

// Serve receives OTLP/HTTP protobuf exports on a local port, writing the
// trace requests to c, and returns its endpoint, for a dagger CLI's
// OTEL_EXPORTER_OTLP_ENDPOINT: the CLI forwards to it every span of its
// session, the engine's included. Logs and metrics are accepted and dropped.
func Serve(c *Capture) (endpoint string, stop func() error, err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := c.Write(&req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(mustMarshal(&coltracepb.ExportTraceServiceResponse{}))
	})
	mux.HandleFunc("POST /v1/logs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(mustMarshal(&collogspb.ExportLogsServiceResponse{}))
	})
	mux.HandleFunc("POST /v1/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(mustMarshal(&colmetricspb.ExportMetricsServiceResponse{}))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	return "http://" + l.Addr().String(), func() error { return srv.Shutdown(context.Background()) }, nil
}

func mustMarshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// FetchCloud writes trace traceID from Dagger Cloud to c, with the Cloud
// authentication of the environment (DAGGER_CLOUD_TOKEN or `dagger login`).
func FetchCloud(ctx context.Context, traceID string, c *Capture) error {
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return err
	}
	if cloudAuth == nil {
		return errors.New("no Dagger Cloud authentication: set DAGGER_CLOUD_TOKEN or run 'dagger login'")
	}
	client, err := cloud.NewOTLPClient(ctx, cloudAuth)
	if err != nil {
		return err
	}
	return client.FetchTrace(ctx, traceID, cloudSink{c})
}

type cloudSink struct{ c *Capture }

func (s cloudSink) ImportSpans(_ context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	return s.c.Write(req)
}

func (cloudSink) ImportLogs(context.Context, *collogspb.ExportLogsServiceRequest) error {
	return nil
}

func (cloudSink) ImportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}

// Seal is a no-op: the replay ends what never ended at the trace's end.
func (cloudSink) Seal(context.Context) error { return nil }

// readCapture calls fn with each request of the capture at path.
func readCapture(path string, fn func(*coltracepb.ExportTraceServiceRequest) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	switch {
	case strings.HasSuffix(path, ".otlp"):
		var buf []byte
		for {
			n, err := binary.ReadUvarint(r)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if uint64(cap(buf)) < n {
				buf = make([]byte, n)
			}
			buf = buf[:n]
			if _, err := io.ReadFull(r, buf); err != nil {
				return err
			}
			var req coltracepb.ExportTraceServiceRequest
			if err := proto.Unmarshal(buf, &req); err != nil {
				return err
			}
			if err := fn(&req); err != nil {
				return err
			}
		}
	case strings.HasSuffix(path, ".jsonl"):
		for {
			line, err := r.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				var req coltracepb.ExportTraceServiceRequest
				if err := protojson.Unmarshal(line, &req); err != nil {
					return err
				}
				if err := fn(&req); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s: a capture is a .otlp or .jsonl file", path)
	}
}
