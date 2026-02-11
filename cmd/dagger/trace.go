package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
	"github.com/vito/go-sse/sse"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"
)

var traceCmd = &cobra.Command{
	Use:    "trace [trace ID]",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Annotations: map[string]string{
		"experimental": "true",
	},
	Aliases: []string{"t"},
	Short:   "View a Dagger trace from Dagger Cloud.",
	Example: `dagger trace 2f123ba77bf7bd2d4db2f70ed20613e8`,
	RunE:    Trace,
}

func Trace(cmd *cobra.Command, args []string) error {
	traceID := args[0]

	return Frontend.Run(cmd.Context(), dagui.FrontendOpts{
		Verbosity: dagui.ShowCompletedVerbosity,
		NoExit:    true,
	}, func(ctx context.Context) (cleanups.CleanupF, error) {
		cloudClient, err := newCloudSSEClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("cloud auth: %w", err)
		}

		eg, ctx := errgroup.WithContext(ctx)

		eg.Go(func() error {
			return cloudClient.streamTraces(ctx, traceID, Frontend, Frontend.SpanExporter())
		})
		eg.Go(func() error {
			return cloudClient.streamLogs(ctx, traceID, Frontend.LogExporter())
		})
		eg.Go(func() error {
			return cloudClient.streamMetrics(ctx, traceID, Frontend.MetricExporter())
		})

		noop := func() error { return nil }

		if err := eg.Wait(); err != nil {
			return noop, err
		}

		return noop, nil
	})
}

type cloudSSEClient struct {
	httpClient *http.Client
	baseURL    string
	authHeader string
}

func newCloudSSEClient(ctx context.Context) (*cloudSSEClient, error) {
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return nil, err
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
	}

	var authHeader string
	switch cloudAuth.Token.TokenType {
	case "Basic":
		header, err := auth.GetDaggerCloudAuth(ctx, cloudAuth.Token.AccessToken)
		if err != nil {
			return nil, err
		}
		authHeader = header
	default:
		authHeader = cloudAuth.Token.Type() + " " + cloudAuth.Token.AccessToken
	}

	baseURL := os.Getenv("DAGGER_CLOUD_URL")
	if baseURL == "" {
		baseURL = "https://api.dagger.cloud"
	}

	return &cloudSSEClient{
		httpClient: http.DefaultClient,
		baseURL:    baseURL,
		authHeader: authHeader,
	}, nil
}

func (c *cloudSSEClient) streamTraces(ctx context.Context, traceID string, fe idtui.Frontend, exp sdktrace.SpanExporter) error {
	return c.consumeSSE(ctx, "/v1/traces/"+traceID, func(data []byte) error {
		slog.Debug("unmarshaling traces", "dataLen", len(data))
		var req coltracepb.ExportTraceServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal traces: %w", err)
		}
		spans := telemetry.SpansFromPB(req.GetResourceSpans())
		if len(spans) == 0 {
			slog.Debug("no spans in traces payload")
			return nil
		}
		slog.Debug("exporting spans from cloud", "spans", len(spans), "resourceSpans", len(req.GetResourceSpans()))
		if err := exp.ExportSpans(ctx, spans); err != nil {
			return fmt.Errorf("export %d spans: %w", len(spans), err)
		}
		slog.Debug("exported spans from cloud", "spans", len(spans))
		// Find the root span (no parent) and zoom the frontend to it,
		// so the TUI shows the trace tree rooted there instead of a
		// flat list of all spans.
		for _, span := range spans {
			if !span.Parent().SpanID().IsValid() {
				spanID := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
				slog.Debug("setting primary span", "spanID", spanID)
				fe.SetPrimary(spanID)
				break
			}
		}
		return nil
	})
}

func (c *cloudSSEClient) streamLogs(ctx context.Context, traceID string, exp sdklog.Exporter) error {
	return c.consumeSSE(ctx, "/v1/logs/"+traceID, func(data []byte) error {
		slog.Debug("unmarshaling logs", "dataLen", len(data))
		var req collogspb.ExportLogsServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal logs: %w", err)
		}
		slog.Debug("re-exporting logs from cloud", "resourceLogs", len(req.GetResourceLogs()))
		if err := telemetry.ReexportLogsFromPB(ctx, exp, &req); err != nil {
			return fmt.Errorf("re-export logs: %w", err)
		}
		slog.Debug("re-exported logs from cloud")
		return nil
	})
}

func (c *cloudSSEClient) streamMetrics(ctx context.Context, traceID string, exp sdkmetric.Exporter) error {
	return c.consumeSSE(ctx, "/v1/metrics/"+traceID, func(data []byte) error {
		slog.Debug("unmarshaling metrics", "dataLen", len(data))
		var req colmetricspb.ExportMetricsServiceRequest
		if err := protojson.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("unmarshal metrics: %w", err)
		}
		slog.Debug("re-exporting metrics from cloud", "resourceMetrics", len(req.GetResourceMetrics()))
		if err := enginetel.ReexportMetricsFromPB(ctx, []sdkmetric.Exporter{exp}, &req); err != nil {
			return fmt.Errorf("re-export metrics: %w", err)
		}
		slog.Debug("re-exported metrics from cloud")
		return nil
	})
}

func (c *cloudSSEClient) consumeSSE(ctx context.Context, path string, cb func([]byte) error) error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("parse cloud URL: %w", err)
	}
	u.Path = path

	slog.Debug("connecting to cloud SSE", "url", u.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("connect to %s: %s: %s", path, resp.Status, string(body))
	}

	slog.Debug("connected to cloud SSE", "path", path)

	reader := sse.NewReadCloser(resp.Body)
	defer reader.Close()

	for {
		event, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				slog.Debug("cloud SSE stream ended", "path", path, "err", err)
				return nil
			}
			return fmt.Errorf("read SSE event from %s: %w", path, err)
		}
		slog.Debug("received SSE event", "path", path, "event", event.Name, "dataLen", len(event.Data))
		if len(event.Data) == 0 {
			continue
		}
		if err := cb(event.Data); err != nil {
			slog.Warn("error processing SSE event", "path", path, "err", err)
		}
	}
}
