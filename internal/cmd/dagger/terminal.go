package daggercmd

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
)

var (
	terminalListMode bool
	terminalCommand  string
	terminalCopies   []string
	terminalInits    []string
)

//go:embed terminals.graphql
var loadTerminalsQuery string

func init() {
	shellCmd.Flags().BoolVarP(&terminalListMode, "list", "l", false, "List available shells")
	shellCmd.Flags().StringVarP(&terminalCommand, "command", "c", "", "Run a command in the shell non-interactively, and exit with its exit code")
	shellCmd.Flags().StringArrayVar(&terminalCopies, "copy", nil, "Copy a directory into the container, as [PATH=]SOURCE. SOURCE is a local path, Git URL, or other address. PATH defaults to the working directory (repeatable)")
	shellCmd.Flags().StringArrayVar(&terminalInits, "init", nil, "Run a command in the shell before it opens, after --copy. Only its changes to files are kept (repeatable)")
}

var shellCmd = &cobra.Command{
	Use:     "shell [options] [NAME]",
	Aliases: []string{"sh"},
	Annotations: map[string]string{
		visibleAliasesAnnotation: "sh",
	},
	Short: "Open a terminal for a container or directory in your project",
	Long: `Open a terminal for a container or directory in your project.

With -c, write the command to the shell's standard input instead of opening a
terminal. Print its output, and exit with its exit code. If standard input is
not a terminal, read the command from it, as if it was given with -c.

Examples:
  dagger shell -l                             # List all available shells
  dagger shell go:dev                         # Open the go:dev shell
  dagger sh go:dev                            # Use the short command alias
  dagger shell go:dev -c 'go test ./...'      # Run a command in the go:dev shell
  echo 'go test ./...' | dagger shell go:dev  # Read the command from stdin
  dagger shell go:dev --copy /src=. --init 'go mod download'
                                              # Set up the shell before it opens
`,
	Args: func(cmd *cobra.Command, args []string) error {
		if terminalListMode {
			for _, flag := range []string{"command", "copy", "init"} {
				if cmd.Flags().Changed(flag) {
					return fmt.Errorf("--list and --%s cannot be used together", flag)
				}
			}
		}
		if cmd.Flags().Changed("command") && len(args) == 0 {
			return fmt.Errorf("--command requires a shell NAME")
		}
		return cobra.MaximumNArgs(1)(cmd, args)
	},
	RunE: runTerminalCommand,
}

func runTerminalCommand(cmd *cobra.Command, args []string) error {
	if !terminalListMode && len(args) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), `Choose a shell to open.

  dagger shell -l       List available shells
  dagger shell <NAME>   Open a shell from that list`)
		return err
	}

	command, hasCommand := terminalCommand, cmd.Flags().Changed("command")
	if !hasCommand && !terminalListMode && !stdinIsTTY {
		// Tell the user why we wait, in case stdin never closes.
		fmt.Fprintln(cmd.ErrOrStderr(), "reading commands from stdin")
		in, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read commands from stdin: %w", err)
		}
		command, hasCommand = string(in), true
	}

	return withEngine(
		cmd.Context(),
		client.Params{LoadWorkspaceModules: true},
		func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			terminals := dag.CurrentWorkspace().Terminals(dagger.WorkspaceTerminalsOpts{Include: args})
			if terminalListMode {
				return listTerminalTargets(ctx, dag, terminals, cmd)
			}
			copies := make([]dagger.TerminalCopy, 0, len(terminalCopies))
			for _, arg := range terminalCopies {
				path, source, err := parseTerminalCopy(arg)
				if err != nil {
					return err
				}
				copies = append(copies, dagger.TerminalCopy{Path: path, Source: dag.Address(source).Directory()})
			}
			if hasCommand {
				return execTerminalCommand(ctx, cmd, terminals.Exec(command, dagger.TerminalGroupExecOpts{
					Copy: copies,
					Init: terminalInits,
				}))
			}
			_, err := terminals.Run(dagger.TerminalGroupRunOpts{Copy: copies, Init: terminalInits}).ID(ctx)
			return err
		},
	)
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
	// Sync once: each query of the exec field runs the command again.
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

func listTerminalTargets(ctx context.Context, dag *dagger.Client, terminals *dagger.TerminalGroup, cmd *cobra.Command) error {
	list, err := loadGroupListDetails(ctx, dag, "fetch terminal information",
		func(ctx context.Context) (any, error) { return terminals.ID(ctx) },
		loadTerminalsQuery, "TerminalGroupListDetails",
	)
	if err != nil {
		return err
	}
	items := make([]commandListItem, 0, len(list))
	for _, terminal := range list {
		items = append(items, commandListItem{
			Name:    cliName(terminal.Name),
			Comment: firstDescriptionLine(terminal.Description),
		})
	}
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(out, "# select with 'dagger shell <NAME>'"); err != nil {
		return err
	}
	return writeCommandList(out, items)
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
