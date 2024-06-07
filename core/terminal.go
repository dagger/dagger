package core

import (
	"context"
	"errors"
	"fmt"
	"io"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkgwpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/muesli/termenv"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/idtui"
)

const terminalPrompt = `PS1=\\033[38;5;11mdagger:\w$ \\033[0m`

type TerminalArgs struct {
	Cmd []string `default:"[]"`

	// Provide dagger access to the executed command
	// Do not use this option unless you trust the command being executed.
	// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
	ExperimentalPrivilegedNesting *bool `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities *bool `default:"false"`
}

func (container *Container) Terminal(
	ctx context.Context,
	svcID *call.ID,
	args *TerminalArgs,
) error {
	container = container.Clone()

	container, err := container.WithExec(ctx, ContainerExecOpts{
		Args:                          args.Cmd,
		SkipEntrypoint:                true,
		ExperimentalPrivilegedNesting: *args.ExperimentalPrivilegedNesting,
		InsecureRootCapabilities:      *args.InsecureRootCapabilities,
	})
	if err != nil {
		return fmt.Errorf("failed to create container for interactive terminal: %w", err)
	}

	return container.terminal(ctx, svcID, args)
}

func (dir *Directory) Terminal(
	ctx context.Context,
	svcID *call.ID,
	ctr *Container,
	args *TerminalArgs,
) error {
	var err error

	if ctr == nil {
		ctr, err = NewContainer(dir.Query, dir.Platform)
		if err != nil {
			return err
		}
		ctr, err = ctr.From(ctx, "alpine:3.20")
		if err != nil {
			return err
		}
	}

	ctr = ctr.Clone()

	ctr.Config.WorkingDir = "/src"
	ctr, err = ctr.WithMountedDirectory(ctx, "/src", dir, "", true)
	if err != nil {
		return err
	}

	ctr, err = ctr.WithExec(ctx, ContainerExecOpts{
		Args:                          args.Cmd,
		SkipEntrypoint:                true,
		ExperimentalPrivilegedNesting: *args.ExperimentalPrivilegedNesting,
		InsecureRootCapabilities:      *args.InsecureRootCapabilities,
	})
	if err != nil {
		return fmt.Errorf("failed to create container for interactive terminal: %w", err)
	}

	return ctr.terminal(ctx, svcID, args)
}

func (container *Container) terminal(
	ctx context.Context,
	svcID *call.ID,
	args *TerminalArgs,
) error {
	container = container.Clone()
	container.Config.Env = append(container.Config.Env, terminalPrompt)
	container, err := container.WithExec(ctx, ContainerExecOpts{
		Args:                          args.Cmd,
		SkipEntrypoint:                true,
		ExperimentalPrivilegedNesting: *args.ExperimentalPrivilegedNesting,
		InsecureRootCapabilities:      *args.InsecureRootCapabilities,
	})
	if err != nil {
		return fmt.Errorf("failed to create container for interactive terminal: %w", err)
	}

	svc, err := container.Service(ctx)
	if err != nil {
		return fmt.Errorf("failed to create service for interactive terminal: %w", err)
	}

	bk := container.Query.Buildkit
	term, err := bk.OpenTerminal(ctx)
	if err != nil {
		return fmt.Errorf("failed to open terminal: %w", err)
	}

	fmt.Fprintf(term.Stderr, "Attaching interactive terminal: ")
	idtui.DumpID(idtui.NewOutput(term.Stderr, termenv.WithTTY(true)), svcID)
	fmt.Fprintf(term.Stderr, "\r\n\n")
	eg, egctx := errgroup.WithContext(ctx)
	runningSvc, err := svc.Start(
		ctx,
		svcID,
		true,
		func(stdin io.Writer, svcProc bkgw.ContainerProcess) {
			eg.Go(func() error {
				_, err := io.Copy(stdin, term.Stdin)
				if err != nil {
					return fmt.Errorf("error forwarding terminal stdin to container: %w", err)
				}
				return nil
			})
			eg.Go(func() error {
				for resize := range term.ResizeCh {
					err := svcProc.Resize(egctx, resize)
					if err != nil {
						return fmt.Errorf("failed to resize terminal: %w", err)
					}
				}
				return nil
			})
		},
		func(stdout io.Reader) {
			eg.Go(func() error {
				defer term.Stdout.Close()
				_, err := io.Copy(term.Stdout, stdout)
				if err != nil {
					if errors.Is(err, io.ErrClosedPipe) {
						return nil
					}
					return fmt.Errorf("error forwarding container stdout to terminal: %w", err)
				}
				return nil
			})
		},
		func(stderr io.Reader) {
			eg.Go(func() error {
				defer term.Stderr.Close()
				_, err := io.Copy(term.Stderr, stderr)
				if err != nil {
					if errors.Is(err, io.ErrClosedPipe) {
						return nil
					}
					return fmt.Errorf("error forwarding container stderr to terminal: %w", err)
				}
				return nil
			})
		},
	)
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	eg.Go(func() error {
		err := <-term.ErrCh
		if err != nil {
			runningSvc.Stop(egctx, true)
		}
		return err
	})

	eg.Go(func() error {
		waitErr := runningSvc.Wait(egctx)
		exitCode := 0
		if waitErr != nil {
			exitCode = 1
			var exitErr *bkgwpb.ExitError
			if errors.As(waitErr, &exitErr) {
				exitCode = int(exitErr.ExitCode)
			}
		}

		err := term.Close(exitCode)
		if err != nil {
			return fmt.Errorf("failed to forward exit code: %w", err)
		}
		return nil
	})

	return eg.Wait()
}
