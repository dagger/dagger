package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

const workspaceExecMountPath = "/ws"

var workspaceExecCmd = newWorkspaceExecCmd()

type workspaceExecOptions struct {
	from    string
	include []string
	exclude []string
	noApply bool
}

func newWorkspaceExecCmd() *cobra.Command {
	opts := workspaceExecOptions{from: distconsts.AlpineImage}
	cmd := &cobra.Command{
		Use:   "exec [OPTIONS] [--] COMMAND [ARGS...]",
		Short: "Execute a command in a container with the selected workspace mounted",
		Long: `Execute a command in a container with the selected workspace mounted at /ws.

Run the command in /ws/<workspace cwd>. The command and its arguments are
executed directly. Use an explicit shell, such as sh -c, for shell syntax.
The command's output is shown in Dagger's normal progress output.

By default, show a prompt before applying workspace changes. Use --auto-apply
to apply without a prompt, or --no-apply to preview changes without applying them.`,
		Example: `  dagger ws exec -- go test ./...
  dagger ws exec --from=golang:1.26 -- gofmt -w .
  dagger ws exec --no-apply -- sh -c 'printf "hello\n" > hello.txt'`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			disposition, err := workspaceExecDisposition(autoApply, opts.noApply)
			if err != nil {
				return err
			}
			return withEngine(cmd.Context(), client.Params{
				LoadWorkspaceModules: true,
			}, func(ctx context.Context, engineClient *client.Client) error {
				return runWorkspaceExec(ctx, engineClient.Dagger(), cmd, args, opts, disposition)
			})
		},
	}
	cmd.Flags().StringVar(&opts.from, "from", opts.from, "Base container address")
	cmd.Flags().StringArrayVar(&opts.include, "include", nil, "Include workspace paths that match the glob pattern (repeatable)")
	cmd.Flags().StringArrayVar(&opts.exclude, "exclude", nil, "Exclude workspace paths that match the glob pattern (repeatable)")
	cmd.Flags().BoolVar(&opts.noApply, "no-apply", false, "Compute and show workspace changes without applying them")
	// Stop parsing flags at COMMAND. All later values belong to the executed
	// command, so arguments such as -w do not require a preceding --.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func workspaceExecDisposition(apply, noApply bool) (changesetDisposition, error) {
	if apply && noApply {
		return changesetDispositionPrompt, errors.New("--auto-apply and --no-apply cannot be used together")
	}
	if apply {
		return changesetDispositionApply, nil
	}
	if noApply {
		return changesetDispositionNoApply, nil
	}
	return changesetDispositionPrompt, nil
}

func runWorkspaceExec(
	ctx context.Context,
	dag *dagger.Client,
	cmd *cobra.Command,
	args []string,
	opts workspaceExecOptions,
	disposition changesetDisposition,
) error {
	ctx, execSpan := Tracer().Start(ctx, "workspace exec", telemetry.Passthrough())
	defer execSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: execSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))

	ws := dag.CurrentWorkspace()
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return fmt.Errorf("load workspace cwd: %w", err)
	}
	relCwd, err := workspaceRelativeCwd(cwd)
	if err != nil {
		return err
	}
	containerWorkdir, err := workspaceExecWorkdir(relCwd)
	if err != nil {
		return err
	}

	beforeMount := ws.Directory("/", dagger.WorkspaceDirectoryOpts{
		Include: opts.include,
		Exclude: opts.exclude,
	})
	base := dag.Address(opts.from).Container()
	executed := base.
		WithMountedDirectory(workspaceExecMountPath, beforeMount).
		WithWorkdir(containerWorkdir).
		WithExec(args, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	exitCode, err := executed.ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("execute command: %w", err)
	}

	afterMount := executed.Directory(workspaceExecMountPath)
	updated := ws.WithChanges(afterMount.Changes(beforeMount))
	previewDisposition := disposition
	if exitCode != 0 {
		// Failed commands can be useful even when they produce partial changes,
		// but those changes must never be applied automatically or by a prompt.
		previewDisposition = changesetDispositionNoApply
	}
	previewOut := cmd.ErrOrStderr()
	if previewDisposition == changesetDispositionNoApply {
		// The primary span owns the report output. Attach the preview to it so
		// report and TUI modes show the changes with the command output.
		previewStdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
		defer previewStdio.Close()
		previewOut = previewStdio.Stderr
	}
	_, previewErr := handleWorkspaceResponseWithDisposition(
		ctx,
		dag,
		ws,
		updated,
		previewDisposition,
		previewOut,
	)
	if exitCode != 0 {
		return idtui.ExitError{OriginalCode: exitCode, Original: previewErr}
	}
	return previewErr
}

func workspaceExecWorkdir(relCwd string) (string, error) {
	containerCwd := filepath.ToSlash(relCwd)
	if path.IsAbs(containerCwd) || containerCwd == ".." || len(containerCwd) > 3 && containerCwd[:3] == "../" {
		return "", fmt.Errorf("workspace cwd %q escapes %s", relCwd, workspaceExecMountPath)
	}
	containerWorkdir := workspaceExecMountPath
	if relCwd != "" {
		containerWorkdir = path.Join(workspaceExecMountPath, containerCwd)
	}
	if containerWorkdir != workspaceExecMountPath && !pathHasPrefix(containerWorkdir, workspaceExecMountPath) {
		return "", fmt.Errorf("workspace cwd %q escapes %s", relCwd, workspaceExecMountPath)
	}
	return containerWorkdir, nil
}

func pathHasPrefix(value, prefix string) bool {
	return value == prefix || len(value) > len(prefix) && value[:len(prefix)] == prefix && value[len(prefix)] == '/'
}
