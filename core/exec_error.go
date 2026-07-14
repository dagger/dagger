package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkexecutor "github.com/dagger/dagger/internal/buildkit/executor"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// ExecError is a user-facing exec failure payload.
//
// It supports GraphQL extension serialization via Extensions().
type ExecError struct {
	Err      error
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExecError) Error() string {
	return e.Err.Error()
}

func (e *ExecError) Unwrap() error {
	return e.Err
}

func (e *ExecError) Extensions() map[string]any {
	return map[string]any{
		"_type":    "EXEC_ERROR",
		"cmd":      e.Cmd,
		"exitCode": e.ExitCode,
		"stdout":   e.Stdout,
		"stderr":   e.Stderr,
	}
}

func execErrorFromMetaRef(
	ctx context.Context,
	client *engineutil.Client,
	origin trace.SpanContext,
	cause error,
	meta *bkexecutor.Meta,
	metaRef bkcache.ImmutableRef,
) (*ExecError, bool, error) {
	if metaRef == nil {
		return nil, false, nil
	}

	stdout, stderr, exitCode, err := getExecMeta(ctx, client, metaRef)
	if err != nil {
		return nil, false, err
	}

	spanCtx := origin
	if !spanCtx.IsValid() {
		// User-facing origin: never a profiling span (see dagql.MarkProfilingSpan).
		spanCtx = dagql.UserFacingSpanContext(ctx)
	}

	execErr := &ExecError{
		Err:      telemetry.TrackOrigin(cause, spanCtx),
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(string(stdout)),
		Stderr:   strings.TrimSpace(string(stderr)),
	}
	if meta != nil {
		execErr.Cmd = meta.Args
	}
	return execErr, true, nil
}

func functionCallReturnedError(
	ctx context.Context,
	errID dagql.ID[*Error],
	execErr error,
) (*Error, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("load returned function error: %w", err)
	}

	errInst, err := errID.Load(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("load returned function error: %w", err)
	}

	dagErr := errInst.Self().Clone()

	originCtx := trace.SpanContextFromContext(
		telemetry.Propagator.Extract(
			context.Background(),
			telemetry.AnyMapCarrier(dagErr.Extensions()),
		),
	)
	if !originCtx.IsValid() && execErr != nil {
		if origins := telemetry.ParseErrorOrigins(execErr.Error()); len(origins) > 0 && origins[0].IsValid() {
			originCtx = origins[0]
		}
	}
	if !originCtx.IsValid() {
		// User-facing origin: the resolver runs on the call_exec twin's context,
		// but attribution must name a span frontends render (see
		// dagql.MarkProfilingSpan) — otherwise the CLI prints a redundant
		// trailing Error: block and the fn's logs detach from its row.
		originCtx = dagql.UserFacingSpanContext(ctx)
	}
	if originCtx.IsValid() && len(telemetry.ParseErrorOrigins(dagErr.Message)) == 0 {
		dagErr.Message = telemetry.TrackOrigin(errors.New(dagErr.Message), originCtx).Error()
	}
	if originCtx.IsValid() {
		extOriginCtx := trace.SpanContextFromContext(
			telemetry.Propagator.Extract(
				context.Background(),
				telemetry.AnyMapCarrier(dagErr.Extensions()),
			),
		)
		if !extOriginCtx.IsValid() {
			carrier := propagation.MapCarrier{}
			telemetry.Propagator.Inject(trace.ContextWithSpanContext(context.Background(), originCtx), carrier)
			for _, key := range carrier.Keys() {
				valJSON, err := json.Marshal(carrier.Get(key))
				if err != nil {
					return nil, fmt.Errorf("marshal error extension %q: %w", key, err)
				}
				dagErr = dagErr.WithValue(key, JSON(valJSON))
			}
		}
	}

	return dagErr, nil
}

func getExecMeta(ctx context.Context, client *engineutil.Client, metaMount bkcache.ImmutableRef) (stdout []byte, stderr []byte, exitCode int, _ error) {
	stdout, err := readSnapshotPathFromRef(ctx, client, metaMount, engineutil.MetaMountStdoutPath, engineutil.MaxExecErrorOutputBytes)
	if err != nil {
		return nil, nil, 0, err
	}
	stderr, err = readSnapshotPathFromRef(ctx, client, metaMount, engineutil.MetaMountStderrPath, engineutil.MaxExecErrorOutputBytes)
	if err != nil {
		return nil, nil, 0, err
	}

	exitCodeBytes, err := readSnapshotPathFromRef(ctx, client, metaMount, engineutil.MetaMountExitCodePath, engineutil.MaxExecErrorOutputBytes)
	if err != nil {
		return nil, nil, 0, err
	}
	exitCode = -1
	if len(exitCodeBytes) > 0 {
		exitCode, err = strconv.Atoi(string(exitCodeBytes))
		if err != nil {
			return nil, nil, 0, err
		}
	}

	return stdout, stderr, exitCode, nil
}

func readSnapshotPathFromRef(ctx context.Context, client *engineutil.Client, ref bkcache.ImmutableRef, filePath string, limit int) ([]byte, error) {
	if ref == nil {
		return nil, nil
	}
	mountable, err := ref.Mount(ctx, true)
	if err != nil {
		return nil, err
	}
	return engineutil.ReadSnapshotPath(ctx, client, mountable, filePath, limit)
}
