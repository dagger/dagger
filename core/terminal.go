package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	bkgwpb "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	"github.com/muesli/termenv"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
)

const (
	defaultTerminalImage = distconsts.AlpineImage
)

func cloneContainerForTerminal(ctr *Container) *Container {
	if ctr == nil {
		return nil
	}
	cp := *ctr
	cp.Config.ExposedPorts = maps.Clone(cp.Config.ExposedPorts)
	cp.Config.Env = slices.Clone(cp.Config.Env)
	cp.Config.Entrypoint = slices.Clone(cp.Config.Entrypoint)
	cp.Config.Cmd = slices.Clone(cp.Config.Cmd)
	cp.Config.Volumes = maps.Clone(cp.Config.Volumes)
	cp.Config.Labels = maps.Clone(cp.Config.Labels)
	cp.Mounts = slices.Clone(cp.Mounts)
	cp.Secrets = slices.Clone(cp.Secrets)
	cp.Sockets = slices.Clone(cp.Sockets)
	cp.Ports = slices.Clone(cp.Ports)
	cp.Services = slices.Clone(cp.Services)
	cp.SystemEnvNames = slices.Clone(cp.SystemEnvNames)
	return &cp
}

type ExecTerminalArgs struct {
	Cmd []string `default:"[]"`
}

type TerminalArgs struct {
	ExecTerminalArgs

	// Provide dagger access to the executed command
	ExperimentalPrivilegedNesting dagql.Optional[dagql.Boolean] `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities dagql.Optional[dagql.Boolean] `default:"false"`
}

func (container *Container) Terminal(
	ctx context.Context,
	svcID *call.ID,
	args *TerminalArgs,
) error {
	return container.terminal(ctx, svcID, args, nil)
}

func (container *Container) TerminalError(
	ctx context.Context,
	svcID *call.ID,
	richErr *buildkit.RichError,
) error {
	return container.terminal(ctx, svcID, nil, richErr)
}

func (container *Container) terminal(
	ctx context.Context,
	svcID *call.ID,
	args *TerminalArgs,
	richErr *buildkit.RichError,
) error {
	container = cloneContainerForTerminal(container)

	// HACK: ensure that container is entirely built before interrupting nice
	// progress output with the terminal
	err := container.Evaluate(ctx)
	if err != nil {
		return fmt.Errorf("failed to evaluate container: %w", err)
	}

	term, output, err := prepTerminal(ctx, svcID, richErr)
	if err != nil {
		return err
	}
	defer term.Close(bkgwpb.UnknownExitStatus) // always close term; it's wrapped in a once so it won't be called multiple times
	container.Config.Env = prepTerminalEnv(output, container.Config.Env)

	var svc *Service
	if richErr == nil {
		svc, err = container.AsService(ctx, ContainerAsServiceArgs{
			Args:                          args.Cmd,
			ExperimentalPrivilegedNesting: args.ExperimentalPrivilegedNesting.Value.Bool(),
			InsecureRootCapabilities:      args.InsecureRootCapabilities.Value.Bool(),
		})
	} else {
		svc, err = container.AsRecoveredService(ctx, richErr)
	}
	if err != nil {
		return fmt.Errorf("failed to create service for interactive terminal: %w", err)
	}

	eg, egctx := errgroup.WithContext(ctx)
	runningSvc, err := svc.Start(
		ctx,
		svcID,
		&ServiceIO{
			Stdin:       term.Stdin,
			Stdout:      term.Stdout,
			Stderr:      term.Stderr,
			ResizeCh:    term.ResizeCh,
			Interactive: true,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	eg.Go(func() error {
		err := <-term.ErrCh
		if err != nil {
			runningSvc.Stop(egctx, true)
			return fmt.Errorf("terminal session failed: %w", err)
		}
		return nil
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

func (dir *Directory) Terminal(
	ctx context.Context,
	svcID *call.ID,
	ctr *Container,
	args *TerminalArgs,
	parent dagql.ObjectResult[*Directory],
) error {
	var err error

	if ctr == nil {
		ctr = NewContainer(dir.Platform)
		ctr, err = ctr.FromRefString(ctx, defaultTerminalImage)
		if err != nil {
			return fmt.Errorf("failed to create terminal container: %w", err)
		}
	}

	ctr = cloneContainerForTerminal(ctr)
	ctr.Config.WorkingDir = "/src"
	ctr, err = ctr.WithMountedDirectory(ctx, "/src", parent, "", true)
	if err != nil {
		return fmt.Errorf("failed to create terminal container: %w", err)
	}
	return ctr.Terminal(ctx, svcID, args)
}

func (*Service) Terminal(
	ctx context.Context,
	svc dagql.ObjectResult[*Service],
	args *ExecTerminalArgs,
) error {
	term, output, err := prepTerminal(ctx, svc.ID(), nil)
	if err != nil {
		return err
	}
	defer term.Close(bkgwpb.UnknownExitStatus) // always close term; it's wrapped in a once so it won't be called multiple times

	env := prepTerminalEnv(output, nil)

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return err
	}
	detach, runnings, err := svcs.StartBindings(ctx, ServiceBindings{{Service: svc}})
	if err != nil {
		return err
	}
	defer detach()

	running := runnings[0]
	if running.Exec == nil {
		return fmt.Errorf("service %s does not support terminal", svc.ID().Digest())
	}
	return running.Exec(ctx, args.Cmd, env, &ServiceIO{
		Stdin:       term.Stdin,
		Stdout:      term.Stdout,
		Stderr:      term.Stderr,
		ResizeCh:    term.ResizeCh,
		Interactive: true,
	})
}

func prepTerminal(ctx context.Context, svcID *call.ID, richErr *buildkit.RichError) (*buildkit.TerminalClient, *termenv.Output, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get current query: %w", err)
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	term, err := bk.OpenTerminal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open terminal: %w", err)
	}

	output := idtui.NewOutput(term.Stderr)
	if richErr == nil {
		fmt.Fprint(
			term.Stderr,
			output.String(idtui.DotFilled).Foreground(termenv.ANSIYellow).String()+" Attaching terminal: ",
		)
	} else {
		fmt.Fprint(
			term.Stderr,
			output.String(idtui.IconFailure).Foreground(termenv.ANSIRed).String()+" Exec failed, attaching terminal: ",
		)
	}
	dump := idtui.Dump{Newline: "\r\n", Prefix: "    "}
	fmt.Fprint(term.Stderr, dump.Newline)
	if err := dump.DumpID(output, svcID); err != nil {
		return nil, nil, fmt.Errorf("failed to serialize service ID: %w", err)
	}
	fmt.Fprint(term.Stderr, dump.Newline)
	if richErr != nil {
		fmt.Fprintf(term.Stderr,
			output.String("! %s").Foreground(termenv.ANSIYellow).String(), richErr.Error())
		fmt.Fprint(term.Stderr, dump.Newline)
	}

	return term, output, nil
}

// prepTerminalEnv creates a custom shell prompt `dagger:<cwd>$`
func prepTerminalEnv(output *termenv.Output, env []string) []string {
	env = append(env, fmt.Sprintf("PS1=%s %s $ ",
		output.String("dagger").Foreground(termenv.ANSIYellow).String(),
		output.String(`$(pwd | sed "s|^$HOME|~|")`).Faint().String(),
	))
	return env
}
