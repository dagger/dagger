package buildkit

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql/idtui"
	bkexecutor "github.com/moby/buildkit/executor"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkgwpb "github.com/moby/buildkit/frontend/gateway/pb"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/errdefs"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger/telemetry"
)

// ExecError is a custom dagger error that occurs during a `withExec` execution.
//
// It supports being serialized/deserialized through graphql.
type ExecError struct {
	Err      error
	Origin   trace.SpanContext
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
	ext := map[string]any{
		"_type":    "EXEC_ERROR",
		"cmd":      e.Cmd,
		"exitCode": e.ExitCode,
		"stdout":   e.Stdout,
		"stderr":   e.Stderr,
	}
	ctx := trace.ContextWithSpanContext(context.Background(), e.Origin)
	telemetry.Propagator.Inject(ctx, telemetry.AnyMapCarrier(ext))
	return ext
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
	ExecMD    *ExecutionMetadata
	Meta      *bkexecutor.Meta
	Secretenv []*bksolverpb.SecretEnv
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
		Err:      e,
		Origin:   spanCtx,
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

	// relevant buildkit code we need to contend with here:
	// https://github.com/moby/buildkit/blob/44504feda1ce39bb8578537a6e6a93f90bdf4220/solver/llbsolver/ops/exec.go#L386-L409
	mounts := []ContainerMount{}
	for i, m := range e.Mounts {
		if m.Input == bksolverpb.Empty {
			mounts = append(mounts, ContainerMount{
				Mount: &bkgw.Mount{
					Dest:      m.Dest,
					Selector:  m.Selector,
					Readonly:  m.Readonly,
					MountType: m.MountType,
					CacheOpt:  m.CacheOpt,
					SecretOpt: m.SecretOpt,
					SSHOpt:    m.SSHOpt,
				},
			})
			continue
		}

		// sanity check we don't panic
		if i >= len(e.Mounts) {
			return fmt.Errorf("exec error mount index out of bounds: %d", i)
		}
		errMnt := e.ExecError.Mounts[i]
		if errMnt == nil {
			continue
		}
		workerRef, ok := errMnt.Sys().(*bkworker.WorkerRef)
		if !ok {
			continue
		}

		mounts = append(mounts, ContainerMount{
			WorkerRef: workerRef,
			Mount: &bkgw.Mount{
				Dest:      m.Dest,
				Selector:  m.Selector,
				Readonly:  m.Readonly,
				MountType: m.MountType,
				CacheOpt:  m.CacheOpt,
				SecretOpt: m.SecretOpt,
				SSHOpt:    m.SSHOpt,
				ResultID:  errMnt.ID(),
			},
		})
	}

	dbgCtr, err := client.NewContainer(ctx, NewContainerRequest{
		Hostname: e.Meta.Hostname,
		Mounts:   mounts,
	})
	if err != nil {
		return err
	}
	term, err := client.OpenTerminal(ctx)
	if err != nil {
		return err
	}
	// always close term; it's wrapped in a once so it won't be called multiple times
	defer term.Close(bkgwpb.UnknownExitStatus)

	output := idtui.NewOutput(term.Stderr)
	fmt.Fprint(term.Stderr,
		output.String(idtui.IconFailure).Foreground(termenv.ANSIRed).String()+" Exec failed, attaching terminal: ")
	dump := idtui.Dump{Newline: "\r\n", Prefix: "    "}
	fmt.Fprint(term.Stderr, dump.Newline)
	if err := dump.DumpID(output, execMD.CallID); err != nil {
		return fmt.Errorf("failed to serialize service ID: %w", err)
	}
	fmt.Fprint(term.Stderr, dump.Newline)
	fmt.Fprintf(term.Stderr,
		output.String("! %s").Foreground(termenv.ANSIYellow).String(), e.Error())
	fmt.Fprint(term.Stderr, dump.Newline)

	// We default to "/bin/sh" if the client doesn't provide a command.
	debugCommand := []string{"/bin/sh"}
	if len(client.InteractiveCommand) > 0 {
		debugCommand = client.InteractiveCommand
	}

	eg, ctx := errgroup.WithContext(ctx)

	dbgShell, err := dbgCtr.Start(ctx, bkgw.StartRequest{
		Args: debugCommand,

		Env:          e.Meta.Env,
		Cwd:          e.Meta.Cwd,
		User:         e.Meta.User,
		SecurityMode: e.Meta.SecurityMode,
		SecretEnv:    e.Secretenv,

		Tty:    true,
		Stdin:  term.Stdin,
		Stdout: term.Stdout,
		Stderr: term.Stderr,
	})
	if err != nil {
		return err
	}

	eg.Go(func() error {
		err := <-term.ErrCh
		if err != nil {
			return fmt.Errorf("terminal error: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		for resize := range term.ResizeCh {
			err := dbgShell.Resize(ctx, resize)
			if err != nil {
				return fmt.Errorf("failed to resize terminal: %w", err)
			}
		}
		return nil
	})
	eg.Go(func() error {
		waitErr := dbgShell.Wait()
		termExitCode := 0
		if waitErr != nil {
			termExitCode = 1
			var exitErr *bkgwpb.ExitError
			if errors.As(waitErr, &exitErr) {
				termExitCode = int(exitErr.ExitCode)
			}
		}

		return term.Close(termExitCode)
	})

	return eg.Wait()
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
	return ReadSnapshotPath(ctx, c, mntable, path.Join(MetaMountDestPath, fileName))
}
