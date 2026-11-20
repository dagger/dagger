package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	telemetry "github.com/dagger/otel-go"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// IngestsTelemetry reports whether the engine stores the telemetry this
// client posts about itself (ExportSpans, ExportLogs and ExportMetrics) in
// the client's telemetry store, keeping it out of the client's own telemetry
// streams (engine.ClientTelemetryIngestHeader). It is false for an older
// engine, and for a nested client, whose process already posts its telemetry
// to the engine through its exec's OTLP endpoint.
func (c *Client) IngestsTelemetry() bool {
	return c.ingestsTelemetry
}

// PublishesTelemetryToCloud reports whether the engine publishes the
// session's telemetry to Dagger Cloud on this client's behalf: the client
// asked it to (Params.EngineCloudTelemetry), and the engine confirmed.
func (c *Client) PublishesTelemetryToCloud() bool {
	return c.publishesTelemetry
}

// ExportSpans posts spans this client's process emitted to the engine's
// store for the client. Only call it when IngestsTelemetry is true.
func (c *Client) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	return c.postTelemetry(ctx, "/v1/traces", &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: telemetry.SpansToPB(spans),
	})
}

// ExportLogs is ExportSpans for log records.
func (c *Client) ExportLogs(ctx context.Context, logs []sdklog.Record) error {
	if len(logs) == 0 {
		return nil
	}
	return c.postTelemetry(ctx, "/v1/logs", &collogspb.ExportLogsServiceRequest{
		ResourceLogs: telemetry.LogsToPB(logs),
	})
}

// ExportMetrics is ExportSpans for metrics.
func (c *Client) ExportMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if len(req.GetResourceMetrics()) == 0 {
		return nil
	}
	return c.postTelemetry(ctx, "/v1/metrics", req)
}

func (c *Client) postTelemetry(ctx context.Context, path string, msg proto.Message) error {
	if !c.ingestsTelemetry {
		return fmt.Errorf("post %s: the engine does not store client telemetry", path)
	}
	body, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dagger"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("post %s: %s: %s", path, resp.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
