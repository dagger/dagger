package telemetry

import (
	"context"
	"io"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/stats"
)

// Kinds of external transfers. Bytes are counted at the boundary of each kind:
//   - filesync: gRPC message bytes exchanged with the session's main client;
//   - registry and http: request or response body bytes, before content
//     decoding;
//   - git: IP packet bytes that git commands exchange with external addresses.
//
// Transfers inside the engine, such as with nested clients and Dagger
// services, are not external. Command traffic of executions is in their
// resource readings, not in transfers.
const (
	WorkloadTransferFilesync = "filesync"
	WorkloadTransferRegistry = "registry"
	WorkloadTransferGit      = "git"
	WorkloadTransferHTTP     = "http"
)

// WorkloadScope is the instrumentation scope of the spans that only the
// workload export receives.
const WorkloadScope = "dagger.io/workload"

// WorkloadTracerProvider creates spans that only the workload export receives.
// It shares the engine queue, not the session's CLI or Cloud trace
// destinations, so ordinary telemetry and the UI do not change.
func (d *WorkloadExport) WorkloadTracerProvider(sessionID string) *sdktrace.TracerProvider {
	processor := d.SpanProcessor(sessionID)
	if processor == nil {
		return nil
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(processor),
	)
}

type workloadTracerKey struct{}

func WithWorkloadTracer(ctx context.Context, provider *sdktrace.TracerProvider) context.Context {
	if provider == nil {
		return ctx
	}
	return context.WithValue(ctx, workloadTracerKey{}, provider.Tracer(WorkloadScope))
}

type workloadInputKey struct{}
type workloadOutputKey struct{}

// WorkloadTransfer records one external transfer only in the workload export.
// The byte counters of the transfer add to it through the context. A nil
// transfer records nothing, which keeps the disabled export path free.
type WorkloadTransfer struct {
	span       trace.Span
	bytes      atomic.Int64
	unmeasured atomic.Bool
	end        sync.Once
}

// StartWorkloadInput begins the record of bytes that the engine receives,
// below the current span.
func StartWorkloadInput(ctx context.Context, kind string) (context.Context, *WorkloadTransfer) {
	return startWorkloadTransfer(ctx, workloadInputKey{}, telemetryattrs.WorkloadInputSpan, kind)
}

// StartWorkloadOutput begins the record of bytes that the engine sends,
// below the current span.
func StartWorkloadOutput(ctx context.Context, kind string) (context.Context, *WorkloadTransfer) {
	return startWorkloadTransfer(ctx, workloadOutputKey{}, telemetryattrs.WorkloadOutputSpan, kind)
}

func startWorkloadTransfer(ctx context.Context, key any, name, kind string) (context.Context, *WorkloadTransfer) {
	tracer, _ := ctx.Value(workloadTracerKey{}).(trace.Tracer)
	if tracer == nil {
		return ctx, nil
	}
	_, span := tracer.Start(ctx, name,
		trace.WithAttributes(attribute.String(telemetryattrs.WorkloadTransferKindAttr, kind)))
	transfer := &WorkloadTransfer{span: span}
	return context.WithValue(ctx, key, transfer), transfer
}

func WorkloadInputFromContext(ctx context.Context) *WorkloadTransfer {
	transfer, _ := ctx.Value(workloadInputKey{}).(*WorkloadTransfer)
	return transfer
}

func WorkloadOutputFromContext(ctx context.Context) *WorkloadTransfer {
	transfer, _ := ctx.Value(workloadOutputKey{}).(*WorkloadTransfer)
	return transfer
}

func (t *WorkloadTransfer) Add(bytes int64) {
	if t != nil && bytes > 0 {
		t.bytes.Add(bytes)
	}
}

// Body counts the bytes read from a response body as bytes of the transfer.
func (t *WorkloadTransfer) Body(body io.ReadCloser) io.ReadCloser {
	if t == nil {
		return body
	}
	return workloadTransferBody{ReadCloser: body, transfer: t}
}

type workloadTransferBody struct {
	io.ReadCloser
	transfer *WorkloadTransfer
}

func (b workloadTransferBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.transfer.Add(int64(n))
	return n, err
}

// WorkloadTransferStatsHandler counts gRPC message bytes as bytes of the
// workload transfers in the RPC context: received messages for an input,
// sent messages for an output. A sent message is counted when gRPC queues it.
// It records no ordinary metric.
func WorkloadTransferStatsHandler() stats.Handler { return workloadTransferStats{} }

type workloadTransferStats struct{}

func (workloadTransferStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

func (workloadTransferStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	switch payload := event.(type) {
	case *stats.InPayload:
		WorkloadInputFromContext(ctx).Add(int64(payload.WireLength))
	case *stats.OutPayload:
		WorkloadOutputFromContext(ctx).Add(int64(payload.WireLength))
	}
}

func (workloadTransferStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (workloadTransferStats) HandleConn(context.Context, stats.ConnStats) {}

// Unmeasured reports that some bytes of the transfer could not be counted.
// The record then has no byte count, so its quantity is unknown.
func (t *WorkloadTransfer) Unmeasured() {
	if t != nil {
		t.unmeasured.Store(true)
	}
}

// End completes the record once. A failed transfer keeps the bytes that it
// counted before the failure.
func (t *WorkloadTransfer) End(identity string, err error) {
	if t == nil {
		return
	}
	t.end.Do(func() {
		if !t.unmeasured.Load() {
			t.span.SetAttributes(attribute.Int64(telemetryattrs.WorkloadTransferBytesAttr, t.bytes.Load()))
		}
		if identity != "" {
			t.span.SetAttributes(attribute.String(telemetryattrs.WorkloadTransferIdentityAttr, identity))
		}
		if err != nil {
			t.span.SetStatus(codes.Error, "")
		}
		t.span.End()
	})
}
