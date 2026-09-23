package daggercmd

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/distconsts"
)

var (
	terminalListMode bool
	terminalCommand  string
	terminalCopies   []string
	terminalInits    []string
)

func init() {
	registerArtifactListFlags(shellCmd)
	shellCmd.Flags().BoolVarP(&terminalListMode, "list", "l", false, "List available shells")
	shellCmd.Flags().StringVarP(&terminalCommand, "command", "c", "", "Run a shell `command` and return its exit code")
	shellCmd.Flags().StringArrayVar(&terminalCopies, "copy", nil, "Copy a directory into the container: `[PATH=]SOURCE` (repeatable)")
	shellCmd.Flags().StringArrayVar(&terminalInits, "init", nil, "Run a shell `command` before opening the shell (repeatable)")
}

var shellCmd = &cobra.Command{
	Use:     "shell [FILTERS] [OPTIONS]",
	Aliases: []string{"sh"},
	Annotations: map[string]string{
		visibleAliasesAnnotation: "sh",
	},
	Short: "Open a terminal for a container or directory in your project",
	Args: func(cmd *cobra.Command, args []string) error {
		if terminalListMode {
			for _, flag := range []string{"command", "copy", "init"} {
				if cmd.Flags().Changed(flag) {
					return fmt.Errorf("--list and --%s cannot be used together", flag)
				}
			}
		}
		return cobra.MaximumNArgs(1)(cmd, args)
	},
	RunE: runTerminalCommand,
}

func runTerminalCommand(cmd *cobra.Command, args []string) error {
	var in string
	var execute bool
	if !terminalListMode {
		var piped bool
		var err error
		in, piped, err = readPipedStdin()
		if err != nil {
			return err
		}
		if !cmd.Flags().Changed("command") && piped && strings.TrimSpace(in) == "" {
			return fmt.Errorf("no commands on stdin")
		}
		execute = cmd.Flags().Changed("command") || piped
	}

	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
	if err != nil {
		return err
	}
	return withEngine(
		cmd.Context(),
		params,
		func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			all, err := commandArtifacts(ctx, dag, dag.CurrentWorkspace(), args, true)
			if err != nil {
				return err
			}
			terminals := all.FilterTypes([]string{"Container", "Directory"})
			if terminalListMode {
				return listArtifactSelection(ctx, dag, terminals, cmd)
			}
			target, err := selectShellArtifact(ctx, dag, terminals)
			if err != nil {
				return err
			}
			ctr, err := shellArtifactContainer(ctx, dag, target)
			if err != nil {
				return err
			}
			for _, arg := range terminalCopies {
				path, source, err := parseTerminalCopy(arg)
				if err != nil {
					return err
				}
				ctr = ctr.WithDirectory(path, dag.Address(source).Directory())
			}
			if len(terminalInits) > 0 {
				shell, err := containerShell(ctx, dag, ctr, true)
				if err != nil {
					return err
				}
				for _, command := range terminalInits {
					ctr = ctr.WithExec(append(slices.Clone(shell.Args), command), dagger.ContainerWithExecOpts{
						DisableDaggerInDagger:    !shell.PrivilegedNesting,
						InsecureRootCapabilities: shell.InsecureRootCapabilities,
					})
				}
			}
			if !execute {
				_, err := ctr.Terminal().ID(ctx)
				return err
			}
			shell, err := containerShell(ctx, dag, ctr, cmd.Flags().Changed("command"))
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("command") {
				shell.Args = append(shell.Args, terminalCommand)
			}
			// Cache setup work, but run the final user command on every invocation.
			ctr = ctr.WithEnvVariable("_DAGGER_SHELL_NONCE", rand.Text())
			return execTerminalCommand(ctx, cmd, ctr.WithExec(shell.Args, dagger.ContainerWithExecOpts{
				Stdin:                    in,
				Expect:                   dagger.ReturnTypeAny,
				DisableDaggerInDagger:    !shell.PrivilegedNesting,
				InsecureRootCapabilities: shell.InsecureRootCapabilities,
			}))
		},
	)
}

type shellArtifact struct {
	ID   string
	URI  string
	Type string
}

func selectShellArtifact(ctx context.Context, dag *dagger.Client, terminals *dagger.Artifacts) (shellArtifact, error) {
	items, err := shellArtifacts(ctx, dag, terminals)
	if err != nil {
		return shellArtifact{}, err
	}
	if len(items) == 0 {
		return shellArtifact{}, fmt.Errorf("no shells selected")
	}
	if len(items) == 1 {
		return items[0], nil
	}
	var containers []shellArtifact
	for _, item := range items {
		if item.Type == "Container" {
			containers = append(containers, item)
		}
	}
	if len(containers) == 1 {
		return containers[0], nil
	}
	entrypoint, err := dag.CurrentWorkspace().Entrypoint(ctx)
	if err != nil {
		return shellArtifact{}, err
	}
	if entrypoint != "" {
		entrypointItems, err := shellArtifacts(ctx, dag, terminals.FilterTypes([]string{"Container"}).FilterURI("dag://"+entrypoint+"/**"))
		if err != nil {
			return shellArtifact{}, err
		}
		if len(entrypointItems) == 1 {
			return entrypointItems[0], nil
		}
	}
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = item.URI
	}
	return shellArtifact{}, fmt.Errorf("shell selection matched %d targets: %s; select one, or run 'dagger shell -l' to list them", len(names), strings.Join(names, ", "))
}

func shellArtifacts(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts) ([]shellArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var result struct {
		Node struct{ Items []shellArtifact }
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query ShellArtifacts($id: ID!) { node(id: $id) { ... on Artifacts { items { id uri(dimensionKeys: true, typeAssertion: true) } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &result})
	if err != nil {
		return nil, err
	}
	for i := range result.Node.Items {
		item := &result.Node.Items[i]
		address, err := dagaddress.Parse(item.URI)
		if err != nil {
			return nil, err
		}
		if len(address.Types) == 1 && address.Types[0] == "container" {
			item.Type = "Container"
		} else {
			item.Type = "Directory"
		}
	}
	return result.Node.Items, nil
}

func shellArtifactContainer(ctx context.Context, dag *dagger.Client, target shellArtifact) (*dagger.Container, error) {
	id, err := dagger.Ref[*dagger.Artifact](dag, dagger.ID(target.ID)).Value().ID(ctx)
	if err != nil {
		return nil, err
	}
	if target.Type == "Container" {
		return dagger.Ref[*dagger.Container](dag, id), nil
	}
	// Directory shells use the CLI's default image and the engine's default
	// platform. The Directory API does not expose a source platform.
	dir := dagger.Ref[*dagger.Directory](dag, id)
	return dag.Container().From(distconsts.AlpineImage).
		WithMountedDirectory("/src", dir).
		WithWorkdir("/src"), nil
}

// shellCommand is the part of Command needed for a shell invocation. The
// container supplies the environment and working directory.
type shellCommand struct {
	Args                     []string
	PrivilegedNesting        bool
	InsecureRootCapabilities bool
}

func containerShell(ctx context.Context, dag *dagger.Client, ctr *dagger.Container, batch bool) (shellCommand, error) {
	id, err := ctr.ID(ctx)
	if err != nil {
		return shellCommand{}, err
	}
	var result struct{ Node struct{ Shell shellCommand } }
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query ContainerShell($id: ID!, $batch: Boolean!) { node(id: $id) { ... on Container { shell(batch: $batch) { args privilegedNesting insecureRootCapabilities } } } }`,
		Variables: map[string]any{"id": id, "batch": batch},
	}, &dagger.Response{Data: &result})
	return result.Node.Shell, err
}

// readPipedStdin reads stdin if it is a pipe or a file. It does not read
// terminals or other devices, such as /dev/null.
func readPipedStdin() (string, bool, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", false, fmt.Errorf("stat stdin: %w", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 && !info.Mode().IsRegular() {
		return "", false, nil
	}
	in, err := io.ReadAll(stdin)
	if err != nil {
		return "", false, fmt.Errorf("read stdin: %w", err)
	}
	return string(in), true, nil
}

// parseTerminalCopy parses a --copy value, [PATH=]SOURCE. SOURCE can contain
// '=', for example in a URL query, so split only if PATH has no ':', '?' or '#'.
func parseTerminalCopy(arg string) (path, source string, _ error) {
	path, source = ".", arg
	if before, after, ok := strings.Cut(arg, "="); ok && !strings.ContainsAny(before, ":?#") {
		path, source = before, after
	}
	if path == "" || source == "" {
		return "", "", fmt.Errorf("invalid --copy %q: expected [PATH=]SOURCE", arg)
	}
	return path, source, nil
}

func execTerminalCommand(ctx context.Context, cmd *cobra.Command, exec *dagger.Container) error {
	// Pin the result before reading its exit code and output.
	executed, err := exec.Sync(ctx)
	if err != nil {
		return err
	}
	exitCode, err := executed.ExitCode(ctx)
	if err != nil {
		return err
	}
	stdout, err := executed.Stdout(ctx)
	if err != nil {
		return err
	}
	stderr, err := executed.Stderr(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprint(cmd.OutOrStdout(), stdout); err != nil {
		return err
	}
	if _, err := fmt.Fprint(cmd.ErrOrStderr(), stderr); err != nil {
		return err
	}
	if exitCode != 0 {
		return idtui.ExitError{OriginalCode: exitCode}
	}
	return nil
}

var terminalMu sync.Mutex

func withTerminal(fn func(stdin io.Reader, stdout, stderr io.Writer) error) error {
	// only allow one terminal session at a time
	terminalMu.Lock()
	defer terminalMu.Unlock()

	if silent {
		return fmt.Errorf("running shell in silent mode is not supported")
	}
	return Frontend.Background(&terminalSession{
		fn: fn,
	}, true)
}

type terminalSession struct {
	fn func(stdin io.Reader, stdout io.Writer, stderr io.Writer) error

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

var _ idtui.ExecCommand = (*terminalSession)(nil)

func (ts *terminalSession) SetStdin(r io.Reader) {
	ts.stdin = r
}

func (ts *terminalSession) SetStdout(w io.Writer) {
	ts.stdout = w
}

func (ts *terminalSession) SetStderr(w io.Writer) {
	ts.stderr = w
}

func (ts *terminalSession) Run() error {
	return ts.fn(ts.stdin, ts.stdout, ts.stderr)
}
