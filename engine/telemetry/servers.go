package telemetry

import (
	"context"
	"sync"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/trace"
	colllogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	colltracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"

	"github.com/dagger/dagger/engine/slog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type TraceServer struct {
	PubSub *PubSub

	*colltracev1.UnimplementedTraceServiceServer
	*UnimplementedTracesSourceServer
}

func (e *TraceServer) Export(ctx context.Context, req *colltracev1.ExportTraceServiceRequest) (*colltracev1.ExportTraceServiceResponse, error) {
	err := e.PubSub.Spans().ExportSpans(ctx, telemetry.SpansFromPB(req.GetResourceSpans()))
	if err != nil {
		return nil, err
	}
	return &colltracev1.ExportTraceServiceResponse{}, nil
}

func (e *TraceServer) Subscribe(req *SubscribeRequest, srv TracesSource_SubscribeServer) error {
	exp, err := otlptrace.New(srv.Context(), &traceStreamExporter{stream: srv, clientID: req.GetClientId()})
	if err != nil {
		return err
	}
	return e.PubSub.SubscribeToSpans(srv.Context(), Topic{
		TraceID:  trace.TraceID(req.GetTraceId()),
		ClientID: req.GetClientId(),
	}, exp)
}

type traceStreamExporter struct {
	stream   TracesSource_SubscribeServer
	clientID string
	mu       sync.Mutex
}

var _ otlptrace.Client = (*traceStreamExporter)(nil)

func (s *traceStreamExporter) Start(ctx context.Context) error {
	return nil
}

func (s *traceStreamExporter) Stop(ctx context.Context) error {
	return nil
}

func (s *traceStreamExporter) UploadTraces(ctx context.Context, spans []*otlptracev1.ResourceSpans) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(&otlptracev1.TracesData{
		ResourceSpans: spans,
	})
}

type LogsServer struct {
	PubSub *PubSub

	*colllogsv1.UnimplementedLogsServiceServer
	*UnimplementedLogsSourceServer
}

func (e *LogsServer) Export(ctx context.Context, req *colllogsv1.ExportLogsServiceRequest) (*colllogsv1.ExportLogsServiceResponse, error) {
	err := e.PubSub.Logs().Export(ctx, telemetry.LogsFromPB(req.GetResourceLogs()))
	if err != nil {
		return nil, err
	}
	return &colllogsv1.ExportLogsServiceResponse{}, nil
}

func (e *LogsServer) Subscribe(req *SubscribeRequest, stream LogsSource_SubscribeServer) error {
	return e.PubSub.SubscribeToLogs(stream.Context(), Topic{
		TraceID:  trace.TraceID(req.GetTraceId()),
		ClientID: req.GetClientId(),
	}, &logStreamExporter{
		stream: stream,
	})
}

type logStreamExporter struct {
	stream LogsSource_SubscribeServer
	mu     sync.Mutex
}

var _ sdklog.Exporter = (*logStreamExporter)(nil)

func (s *logStreamExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(&otlplogsv1.LogsData{
		ResourceLogs: telemetry.LogsToPB(logs),
	})
}

func (s *logStreamExporter) Shutdown(ctx context.Context) error {
	return nil
}

func (s *logStreamExporter) ForceFlush(ctx context.Context) error {
	return nil
}

type MetricsServer struct {
	PubSub *PubSub

	*collmetricsv1.UnimplementedMetricsServiceServer
	*UnimplementedMetricsSourceServer
}

func (e *MetricsServer) Export(ctx context.Context, req *collmetricsv1.ExportMetricsServiceRequest) (*collmetricsv1.ExportMetricsServiceResponse, error) {
	// TODO
	slog.Warn("MetricsServer.Export ignoring export (TODO)")
	return &collmetricsv1.ExportMetricsServiceResponse{}, nil
}

func (e *MetricsServer) Subscribe(req *SubscribeRequest, srv MetricsSource_SubscribeServer) error {
	return status.Errorf(codes.Unimplemented, "Subscribe not implemented")
}
