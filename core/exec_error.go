package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
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

	// ModuleProcess marks the exec of a module entrypoint's module process
	// that has a function error file (engineutil.FunctionErrorPathEnv). Its
	// failure is the function call's: the call fails with FunctionError,
	// or, when the process wrote none, as when it died before its function
	// returned, with this error, values and all.
	ModuleProcess bool
	// FunctionError is the error the module process reported in its
	// function error file, if any.
	FunctionError *FunctionErrorReport
}

const (
	// ModuleProcessExtension is the extension that marks the ExecError of a
	// module process (ExecError.ModuleProcess).
	ModuleProcessExtension = "moduleProcess"
	// FunctionErrorExtension is the extension of a module process's
	// ExecError that holds its FunctionError, which the entrypoint's call
	// fails with.
	FunctionErrorExtension = "functionError"
)

// FunctionErrorReport is the error a module process reports in its function
// error file (engineutil.FunctionErrorPathEnv), which holds one of:
//
//   - {"message": "...", "values": {"name": <any JSON>, ...}}: the error's
//     message, in full, any origin it names ([traceparent:...]) included,
//     and its values, such as an API error's extensions;
//   - {"id": "<Error ID>"}: a Dagger Error the process built, as it would
//     return with FunctionCall.returnError;
//   - anything else: the whole text is the error's message.
type FunctionErrorReport struct {
	Message string                     `json:"message,omitempty"`
	Values  map[string]json.RawMessage `json:"values,omitempty"`
	ID      string                     `json:"id,omitempty"`
}

// ParseFunctionErrorReport reads the content of a function error file, and
// returns nil for an empty one.
//
// Only an object that is exactly one of the structured forms is read as one:
// any other field, an "id" alongside a message or one that is no Error ID,
// and the content is plain text, so a message that happens to be JSON, such
// as an HTTP API's error body, still reaches the caller whole.
func ParseFunctionErrorReport(content []byte) *FunctionErrorReport {
	text := strings.TrimSpace(string(content))
	if text == "" {
		return nil
	}
	if report, ok := parseStructuredFunctionError(text); ok {
		return report
	}
	return &FunctionErrorReport{Message: text}
}

func parseStructuredFunctionError(text string) (*FunctionErrorReport, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var report FunctionErrorReport
	if err := dec.Decode(&report); err != nil || dec.More() {
		return nil, false
	}
	switch {
	case report.ID != "":
		if report.Message != "" || report.Values != nil {
			return nil, false
		}
		var id dagql.ID[*Error]
		if err := id.Decode(report.ID); err != nil {
			return nil, false
		}
		return &report, true
	case report.Message != "":
		return &report, true
	default:
		return nil, false
	}
}

// Return makes the reported error the one fnCall returns, as a module process
// returns one with FunctionCall.returnError: the Error the report names by
// ID, or one with its message and its values, sorted by name. The call that
// fails with it gives it its origin, as it does a returned Error's (see
// functionCallErrorWithOrigin).
func (report *FunctionErrorReport) Return(ctx context.Context, fnCall *FunctionCall) error {
	if report.ID != "" {
		var id dagql.ID[*Error]
		if err := id.Decode(report.ID); err != nil {
			return fmt.Errorf("decode reported function error: %w", err)
		}
		return fnCall.ReturnError(ctx, id)
	}
	dagErr := NewError(report.Message)
	for _, name := range slices.Sorted(maps.Keys(report.Values)) {
		dagErr = dagErr.WithValue(name, JSON(report.Values[name]))
	}
	return fnCall.setReturn(functionCallReturn{Error: dagErr, HasError: true})
}

func (e *ExecError) Error() string {
	return e.Err.Error()
}

func (e *ExecError) Unwrap() error {
	return e.Err
}

func (e *ExecError) Extensions() map[string]any {
	ext := map[string]any{
		"_type":    "EXEC_ERROR",
		"cmd":      e.Cmd,
		"exitCode": e.ExitCode,
		"stdout":   e.Stdout,
		"stderr":   e.Stderr,
	}
	if e.ModuleProcess {
		ext[ModuleProcessExtension] = true
	}
	if e.FunctionError != nil {
		ext[FunctionErrorExtension] = e.FunctionError
	}
	return ext
}

func execErrorFromMetaRef(
	ctx context.Context,
	client *engineutil.Client,
	origin trace.SpanContext,
	cause error,
	meta *bkexecutor.Meta,
	metaRef bkcache.ImmutableRef,
	functionErrorFile bool,
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
	if functionErrorFile {
		mountable, err := metaRef.Mount(ctx, true)
		if err != nil {
			return nil, false, err
		}
		// A file over the limit is no report: the call fails with the exec's
		// own error, as when the process wrote none.
		content, err := engineutil.ReadSnapshotSmallFile(ctx, mountable,
			path.Join(engineutil.MetaMountFunctionErrorDir, engineutil.FunctionErrorFileName),
			engineutil.MaxFunctionErrorBytes)
		if err != nil {
			return nil, false, err
		}
		execErr.ModuleProcess = true
		execErr.FunctionError = ParseFunctionErrorReport(content)
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

	return functionCallErrorWithOrigin(ctx, errInst.Self().Clone(), execErr)
}

// functionCallErrorWithOrigin gives dagErr, the error a function call
// returned, its origin, the span it comes from: the one its values name (a
// traceparent value), else the one its message names, else the one execErr,
// the failure of the call's runtime, names, else the function call itself.
// The message and the values both name it, unless they name one already.
func functionCallErrorWithOrigin(
	ctx context.Context,
	dagErr *Error,
	execErr error,
) (*Error, error) {
	originCtx := trace.SpanContextFromContext(
		telemetry.Propagator.Extract(
			context.Background(),
			telemetry.AnyMapCarrier(dagErr.Extensions()),
		),
	)
	if !originCtx.IsValid() {
		if origins := telemetry.ParseErrorOrigins(dagErr.Message); len(origins) > 0 && origins[0].IsValid() {
			originCtx = origins[0]
		}
	}
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
