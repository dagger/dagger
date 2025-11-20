package buildkit

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	bkexecutor "github.com/dagger/dagger/internal/buildkit/executor"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	bksolver "github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/llbsolver/errdefs"
	bksolverpb "github.com/dagger/dagger/internal/buildkit/solver/pb"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
)

// ExecError is a custom dagger error that occurs during a `withExec` execution.
//
// It supports being serialized/deserialized through graphql.
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

// RichError is an error that can occur while processing a container. It
// contains functionality to allow launching a debug terminal in it, and
// unwrapping more interesting metadata.
//
// TODO: this should be capable of handling other DagOp errors
type RichError struct {
	// HACK: wrap the buildkit exec error, which tracks inputs/mounts. This is
	// required because buildkit has *special* tracking of this type in the
	// solver (that handles gc).
	*errdefs.ExecError

	Origin trace.SpanContext

	// Mounts tracks how the refs are mounted.
	Mounts []*bksolverpb.Mount

	// optional info about the execution that failed
	ExecMD *ExecutionMetadata
	Meta   *bkexecutor.Meta

	Terminal func(ctx context.Context, richErr *RichError) error
}

func (e RichError) Unwrap() error {
	return e.ExecError
}

func (e RichError) AsExecErr(ctx context.Context, client *Client) (*ExecError, bool, error) {
	// This was an exec error, we will retrieve the exec's output and include
	// it in the error message
	// get the mnt corresponding to the metadata where stdout/stderr are stored
	var metaMountResult bksolver.Result
	for i, mnt := range e.Mounts {
		if mnt.Dest == MetaMountDestPath {
			metaMountResult = e.ExecError.Mounts[i]
			break
		}
	}
	if metaMountResult == nil {
		return nil, false, nil
	}
	stdout, stderr, exitCode, err := getExecMeta(ctx, client, metaMountResult)
	if err != nil {
		return nil, false, err
	}

	// Embed the error origin, either from when the op was built, or from the
	// current context if none is found.
	spanCtx := e.Origin
	if !spanCtx.IsValid() {
		spanCtx = trace.SpanContextFromContext(ctx)
	}

	execErr := &ExecError{
		Err:      telemetry.TrackOrigin(e, spanCtx),
		Cmd:      e.Meta.Args,
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(string(stdout)),
		Stderr:   strings.TrimSpace(string(stderr)),
	}
	return execErr, true, nil
}

func (e RichError) DebugTerminal(ctx context.Context, client *Client) error {
	if !client.Interactive {
		return nil
	}

	// Ensure we only spawn one terminal per exec.
	execMD := e.ExecMD
	if execMD.ExecID != "" {
		if _, exists := client.execMap.LoadOrStore(execMD.ExecID, struct{}{}); exists {
			return nil
		}
	}

	// If this is the (internal) exec of the module itself, we don't want to spawn a terminal.
	if execMD.Internal {
		return nil
	}

	meta := *e.Meta
	meta.Args = []string{"/bin/sh"}
	if len(client.InteractiveCommand) > 0 {
		meta.Args = client.InteractiveCommand
	}
	e.Meta = &meta

	return e.Terminal(ctx, &e)
}

func getExecMeta(ctx context.Context, client *Client, metaMount bksolver.Result) (stdout []byte, stderr []byte, exitCode int, _ error) {
	workerRef, ok := metaMount.Sys().(*bkworker.WorkerRef)
	if !ok {
		return nil, nil, 0, fmt.Errorf("invalid ref type: %T", metaMount.Sys())
	}
	if workerRef.ImmutableRef == nil {
		return nil, nil, 0, fmt.Errorf("invalid nil ref")
	}
	mntable, err := workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(client.ID()))
	if err != nil {
		return nil, nil, 0, err
	}

	stdout, err = getExecMetaFile(ctx, client, mntable, MetaMountStdoutPath)
	if err != nil {
		return nil, nil, 0, err
	}
	stderr, err = getExecMetaFile(ctx, client, mntable, MetaMountStderrPath)
	if err != nil {
		return nil, nil, 0, err
	}

	exitCodeBytes, err := getExecMetaFile(ctx, client, mntable, MetaMountExitCodePath)
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

func getExecMetaFile(ctx context.Context, c *Client, mntable snapshot.Mountable, fileName string) ([]byte, error) {
	return ReadSnapshotPath(ctx, c, mntable, fileName, MaxExecErrorOutputBytes)
}
