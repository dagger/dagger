package core

import (
	"context"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkexecutor "github.com/dagger/dagger/internal/buildkit/executor"
	telemetry "github.com/dagger/otel-go"
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

type ModuleExecError struct {
	Err     error
	ErrorID dagql.ID[*Error]
}

func (e *ModuleExecError) Error() string {
	if e == nil || e.Err == nil {
		return "module exec error"
	}
	return e.Err.Error()
}

func (e *ModuleExecError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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
		spanCtx = trace.SpanContextFromContext(ctx)
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

func moduleErrorIDFromRef(
	ctx context.Context,
	client *engineutil.Client,
	ref bkcache.ImmutableRef,
) (dagql.ID[*Error], bool, error) {
	var id dagql.ID[*Error]
	if ref == nil {
		return id, false, nil
	}
	idBytes, err := readSnapshotPathFromRef(ctx, client, ref, modMetaErrorPath, -1)
	if err != nil {
		return id, false, err
	}
	if strings.TrimSpace(string(idBytes)) == "" {
		return id, false, nil
	}
	if err := id.Decode(string(idBytes)); err != nil {
		return id, false, err
	}
	return id, true, nil
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
