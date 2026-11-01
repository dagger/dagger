// Package tracesource reads historical traces from equivalent telemetry
// providers -- the engine's retained telemetry store and Dagger Cloud -- behind
// one interface, and selects between them. Selection finishes before any
// records reach an importer; a chosen source never changes mid-import.
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

// Source supports both complete imports and the incremental display protocol
// (priority spans, listened subtrees, a span's own or descendant logs by
// record class). Cloud's OTLPClient implements it directly and the engine's
// Store answers the same selections, so callers drive one loader against
// either.
type Source interface {
	FetchTrace(context.Context, string, cloud.TraceImportSink) error
	FetchSpans(context.Context, string, cloud.SpanSelection, func(context.Context, *coltracepb.ExportTraceServiceRequest) error) error
	FetchLogs(context.Context, string, cloud.LogSelection, func(context.Context, *collogspb.ExportLogsServiceRequest) error) error
}

var _ Source = (*cloud.OTLPClient)(nil)

// Open must not import telemetry. Its cleanup belongs to the caller until all
// incremental reads have stopped, including interactive reads after a report.
type Open func(context.Context) (Source, func() error, error)

// ErrEngineUnavailable marks a failure to reach or provision the local engine
// at all (e.g. no container runtime): it was never asked for the trace, so
// another source may serve it.
var ErrEngineUnavailable = errors.New("engine unavailable")

// Select prefers the engine's retained store. Only an unavailable source may
// fall back. When the fallback fails too, its error also says why the local
// source was not used.
func Select(ctx context.Context, local, remote Open) (Source, func() error, error) {
	source, close, localErr := local(ctx)
	if localErr == nil {
		return source, close, nil
	}
	if close != nil {
		if closeErr := close(); closeErr != nil {
			return nil, nil, errors.Join(localErr, closeErr)
		}
	}
	if !CanFallback(ctx, localErr) {
		return nil, nil, localErr
	}
	source, close, err := remote(ctx)
	if err != nil {
		if close != nil {
			err = errors.Join(err, close())
			close = nil
		}
		// %v, not %w: the local failure is context for the remote one. A
		// quiet engine error (e.g. no container runtime) found in the chain
		// would otherwise be displayed alone, hiding the remote failure.
		err = errors.Join(err, fmt.Errorf("local engine archive: %v", localErr)) //nolint:errorlint // deliberately not wrapped, see above
	}
	return source, close, err
}

// CanFallback classifies only source-opening errors, never errors after import
// starts. Explicit refusals, bad data and cancellation always remain visible.
func CanFallback(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	// The engine never answered, so nothing it said is being hidden. An
	// internal connect timeout is unavailability, not the caller's deadline.
	if errors.Is(err, ErrEngineUnavailable) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, archive.ErrCorrupt) || errors.Is(err, archive.ErrState) {
		return false
	}
	var request *archive.RequestError
	if errors.As(err, &request) {
		if request.StatusCode == http.StatusUnauthorized || request.StatusCode == http.StatusForbidden {
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
