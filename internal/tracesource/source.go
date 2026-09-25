// Package tracesource selects and reads historical telemetry without coupling
// callers to the engine archive or Cloud transport. Selection finishes before
// any records reach an importer; a chosen source never changes mid-import.
package tracesource

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/internal/cloud"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// Source supports both complete imports and the incremental display protocol.
// OTLPClient implements it directly. Selection types describe the shared wire
// semantics; archive adapters translate them without depending on Cloud auth.
type Source interface {
	FetchTrace(context.Context, string, cloud.TraceImportSink) error
	FetchSpans(context.Context, string, cloud.SpanSelection, func(context.Context, *coltracepb.ExportTraceServiceRequest) error) error
	FetchLogs(context.Context, string, cloud.LogSelection, func(context.Context, *collogspb.ExportLogsServiceRequest) error) error
}

// Open must not import telemetry. Its cleanup belongs to the caller until all
// incremental reads have stopped, including interactive reads after a report.
type Open func(context.Context) (Source, func() error, error)

// Select prefers the retained engine archive. Only an unavailable source may
// fall back, and an explicit generation never silently becomes a Cloud trace.
func Select(ctx context.Context, generation string, local, remote Open) (Source, func() error, error) {
	source, close, err := local(ctx)
	if err == nil {
		return source, close, nil
	}
	if close != nil {
		if closeErr := close(); closeErr != nil {
			return nil, nil, errors.Join(err, closeErr)
		}
	}
	if !CanFallback(ctx, err) {
		return nil, nil, err
	}
	if generation != "" {
		return nil, nil, fmt.Errorf("selected engine archive generation %s is unavailable: %w; Cloud has no generation selector", generation, err)
	}
	source, close, err = remote(ctx)
	if err != nil && close != nil {
		err = errors.Join(err, close())
		close = nil
	}
	return source, close, err
}

// CanFallback classifies only source-opening errors, never errors after import
// starts. Explicit refusals, bad data and cancellation always remain visible.
func CanFallback(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, archive.ErrCorrupt) || errors.Is(err, archive.ErrState) {
		return false
	}
	var request *archive.RequestError
	if errors.As(err, &request) {
		if request.Failure == archive.FailureAmbiguous || request.StatusCode == http.StatusUnauthorized || request.StatusCode == http.StatusForbidden {
			return false
		}
		if archive.IsCleanMiss(err) {
			return true
		}
		// Malformed requests and authority errors are not availability failures.
		return request.Kind == archive.ErrorTransient && (request.StatusCode == 0 || request.StatusCode == http.StatusMethodNotAllowed || request.StatusCode == http.StatusNotImplemented || request.StatusCode == http.StatusBadGateway || request.StatusCode == http.StatusServiceUnavailable || request.StatusCode == http.StatusGatewayTimeout)
	}
	return archive.IsCleanMiss(err) || errors.Is(err, archive.ErrTransient)
}
