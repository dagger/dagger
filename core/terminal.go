package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/internal/buildkit/executor"
	bkgwpb "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/engineutil"
)

const (
	defaultTerminalImage = distconsts.AlpineImage
)

func cloneContainerForTerminal(ctx context.Context, query *Query, ctr *Container) (*Container, error) {
	if ctr == nil {
		return nil, nil
	}
	cp := *ctr
	cp.Config.ExposedPorts = maps.Clone(cp.Config.ExposedPorts)
	cp.Config.Env = slices.Clone(cp.Config.Env)
	cp.Config.Entrypoint = slices.Clone(cp.Config.Entrypoint)
	cp.Config.Cmd = slices.Clone(cp.Config.Cmd)
	cp.Config.Volumes = maps.Clone(cp.Config.Volumes)
	cp.Config.Labels = maps.Clone(cp.Config.Labels)
	cp.Lazy = nil
	_ = query
	var err error
	cp.FS, err = cloneTerminalDirectorySource(ctx, query, ctr.FS)
	if err != nil {
		return nil, err
	}
	cp.Mounts, err = cloneTerminalMounts(ctx, query, ctr.Mounts)
	if err != nil {
		return nil, err
	}
	cp.MetaSnapshot, err = CloneContainerMetaSnapshot(ctx, ctr.MetaSnapshot)
	if err != nil {
		return nil, err
	}
	cp.Secrets = slices.Clone(cp.Secrets)
	cp.Sockets = slices.Clone(cp.Sockets)
	cp.Ports = slices.Clone(cp.Ports)
	cp.Services = slices.Clone(cp.Services)
	cp.SystemEnvNames = slices.Clone(cp.SystemEnvNames)
	return &cp, nil
}

func cloneTerminalMounts(ctx context.Context, query *Query, mounts ContainerMounts) (ContainerMounts, error) {
	if mounts == nil {
		return nil, nil
	}
	cp := make(ContainerMounts, len(mounts))
	for i, mnt := range mounts {
		cp[i] = mnt
		var err error
		cp[i].DirectorySource, err = cloneTerminalDirectorySource(ctx, query, mnt.DirectorySource)
		if err != nil {
			return nil, err
		}
		cp[i].FileSource, err = cloneTerminalFileSource(ctx, query, mnt.FileSource)
		if err != nil {
			return nil, err
		}
	}
	return cp, nil
}

func cloneTerminalDirectorySource(ctx context.Context, query *Query, src *LazyAccessor[*Directory, *Container]) (*LazyAccessor[*Directory, *Container], error) {
	_ = query
	return CloneContainerDirectoryAccessor(ctx, src)
}

func cloneTerminalFileSource(ctx context.Context, query *Query, src *LazyAccessor[*File, *Container]) (*LazyAccessor[*File, *Container], error) {
	_ = query
	return CloneContainerFileAccessor(ctx, src)
}

func newSyntheticTerminalContainerResult(
	srv *dagql.Server,
	ctr *Container,
	syntheticOp string,
) (dagql.ObjectResult[*Container], error) {
	return dagql.NewObjectResultForCall(ctr, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		Type:        dagql.NewResultCallType(ctr.Type()),
		SyntheticOp: syntheticOp,
		ImplicitInputs: []*dagql.ResultCallArg{
			{
				Name: "terminalNonce",
				Value: &dagql.ResultCallLiteral{
					Kind:        dagql.ResultCallLiteralKindString,
					StringValue: rand.Text(),
				},
			},
		},
	})
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
	selectedID *call.ID,
	selectedDigest digest.Digest,
	containerRes dagql.ObjectResult[*Container],
	args *TerminalArgs,
) error {
	return container.terminal(ctx, selectedID, selectedDigest, containerRes, args, nil, nil, nil)
}

func (container *Container) TerminalExecError(
	ctx context.Context,
	selectedID *call.ID,
	selectedDigest digest.Digest,
	containerRes dagql.ObjectResult[*Container],
	execMD *engineutil.ExecutionMetadata,
	execMeta *executor.Meta,
	execErr error,
) error {
	return container.terminal(ctx, selectedID, selectedDigest, containerRes, nil, execMD, execMeta, execErr)
}

func (container *Container) terminal(
	ctx context.Context,
	selectedID *call.ID,
	selectedDigest digest.Digest,
	containerRes dagql.ObjectResult[*Container],
	args *TerminalArgs,
	execMD *engineutil.ExecutionMetadata,
	execMeta *executor.Meta,
	execErr error,
) error {
	if containerRes.Self() == nil {
		return fmt.Errorf("terminal container result is nil")
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dagql server: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return fmt.Errorf("terminal attach container: empty session ID")
	}
	attachedAny, err := cache.AttachResult(ctx, clientMetadata.SessionID, srv, containerRes)
	if err != nil {
		return fmt.Errorf("failed to attach terminal container: %w", err)
	}
	attached, ok := attachedAny.(dagql.ObjectResult[*Container])
	if !ok {
		return fmt.Errorf("failed to attach terminal container: expected %T, got %T", containerRes, attachedAny)
	}
	containerRes = attached
	// HACK: ensure that container is entirely built before interrupting nice
	// progress output with the terminal
	if err := cache.Evaluate(ctx, containerRes); err != nil {
		return fmt.Errorf("failed to evaluate container: %w", err)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	container, err = cloneContainerForTerminal(ctx, query, containerRes.Self())
	if err != nil {
		return fmt.Errorf("failed to clone terminal container: %w", err)
	}

	term, output, err := prepTerminal(ctx, selectedID, execErr)
	if err != nil {
		return err
	}
	defer term.Close(bkgwpb.UnknownExitStatus) // always close term; it's wrapped in a once so it won't be called multiple times
	container.Config.Env = prepTerminalEnv(output, container.Config.Env)

	containerRes, err = newSyntheticTerminalContainerResult(srv, container, "terminal_container")
	if err != nil {
		return fmt.Errorf("failed to attach terminal container: %w", err)
	}

	var svc *Service
	if execMD == nil && execMeta == nil {
		svc, err = container.AsService(ctx, containerRes, ContainerAsServiceArgs{
			Args:                          args.Cmd,
			ExperimentalPrivilegedNesting: args.ExperimentalPrivilegedNesting.Value.Bool(),
			InsecureRootCapabilities:      args.InsecureRootCapabilities.Value.Bool(),
		})
	} else {
		svc = &Service{
			Container: containerRes,
			ExecMD:    execMD,
			ExecMeta:  execMeta,
		}
	}
	if err != nil {
		return fmt.Errorf("failed to create service for interactive terminal: %w", err)
	}

	if selectedDigest == "" {
		return fmt.Errorf("terminal selection digest is empty")
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return err
	}
	eg, egctx := errgroup.WithContext(ctx)
	runningSvc, release, err := svcs.StartInteractive(
		ctx,
		selectedDigest,
		svc,
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
	defer release()

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
	selectedID *call.ID,
	selectedDigest digest.Digest,
	ctr dagql.ObjectResult[*Container],
	args *TerminalArgs,
	parent dagql.ObjectResult[*Directory],
) error {
	var err error

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to get dagql server: %w", err)
	}

	if ctr.Self() == nil {
		defaultCtr := NewContainer(dir.Platform)
		ctr, err = defaultCtr.FromRefString(ctx, defaultTerminalImage)
		if err != nil {
			return fmt.Errorf("failed to create terminal container: %w", err)
		}
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cache for terminal container: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return fmt.Errorf("directory terminal attach container: empty session ID")
	}
	attachedAny, err := cache.AttachResult(ctx, clientMetadata.SessionID, srv, ctr)
	if err != nil {
		return fmt.Errorf("failed to attach terminal base container: %w", err)
	}
	attachedCtr, ok := attachedAny.(dagql.ObjectResult[*Container])
	if !ok {
		return fmt.Errorf("failed to attach terminal base container: expected %T, got %T", ctr, attachedAny)
	}
	ctr = attachedCtr
	if err := cache.Evaluate(ctx, ctr); err != nil {
		return fmt.Errorf("failed to evaluate terminal base container: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	termCtr, err := cloneContainerForTerminal(ctx, query, ctr.Self())
	if err != nil {
		return fmt.Errorf("failed to clone terminal base container: %w", err)
	}
	termCtr.Config.WorkingDir = "/src"
	termCtr, err = termCtr.WithMountedDirectory(ctx, ctr, "/src", parent, "", true)
	if err != nil {
		return fmt.Errorf("failed to create terminal container: %w", err)
	}
	termCtrRes, err := newSyntheticTerminalContainerResult(srv, termCtr, "directory_terminal_container")
	if err != nil {
		return fmt.Errorf("failed to attach terminal container: %w", err)
	}
	return termCtr.Terminal(ctx, selectedID, selectedDigest, termCtrRes, args)
}

func (*Service) Terminal(
	ctx context.Context,
	svc dagql.ObjectResult[*Service],
	args *ExecTerminalArgs,
) error {
	svcID, err := svc.ID()
	if err != nil {
		return fmt.Errorf("service terminal ID: %w", err)
	}
	term, output, err := prepTerminal(ctx, svcID, nil)
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
	running, release, err := svcs.StartResultWithDependencyExitPropagationSuppressed(ctx, svc)
	if err != nil {
		return err
	}
	defer release()
	if running.Exec == nil {
		return fmt.Errorf("service %s does not support terminal", svcID.Path())
	}
	return running.Exec(ctx, args.Cmd, env, &ServiceIO{
		Stdin:       term.Stdin,
		Stdout:      term.Stdout,
		Stderr:      term.Stderr,
		ResizeCh:    term.ResizeCh,
		Interactive: true,
	})
}

func prepTerminal(ctx context.Context, svcID *call.ID, execErr error) (*engineutil.TerminalClient, *termenv.Output, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get current query: %w", err)
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get engine client: %w", err)
	}

	term, err := bk.OpenTerminal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open terminal: %w", err)
	}

	output := idtui.NewOutput(term.Stderr)
	if execErr == nil {
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
	dumpID := svcID
	if svcID != nil && svcID.IsHandle() {
		dag, err := CurrentDagqlServer(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get dagql server: %w", err)
		}
		res, err := dag.LoadType(ctx, svcID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load service from handle ID: %w", err)
		}
		recipeIDable, ok := res.(interface {
			RecipeID(context.Context) (*call.ID, error)
		})
		if !ok {
			return nil, nil, fmt.Errorf("loaded service %T does not expose a recipe ID", res)
		}
		dumpID, err = recipeIDable.RecipeID(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to derive service recipe ID: %w", err)
		}
	}
	if err := dump.DumpID(output, dumpID); err != nil {
		return nil, nil, fmt.Errorf("failed to serialize service ID: %w", err)
	}
	fmt.Fprint(term.Stderr, dump.Newline)
	if execErr != nil {
		fmt.Fprintf(term.Stderr,
			output.String("! %s").Foreground(termenv.ANSIYellow).String(), execErr.Error())
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
