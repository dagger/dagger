package daggercmd

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"sync"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
)

var terminalListMode bool

//go:embed terminals.graphql
var loadTerminalsQuery string

func init() {
	terminalCmd.Flags().BoolVarP(&terminalListMode, "list", "l", false, "List available terminal targets")
}

var terminalCmd = &cobra.Command{
	Use:     "terminal [options] [pattern]",
	Aliases: []string{"tty"},
	Annotations: map[string]string{
		visibleAliasesAnnotation: "tty",
	},
	Short: "Open a terminal for a container or directory in your project",
	Long: `Open a terminal for a container or directory in your project.

Examples:
  dagger terminal -l                 # List all available terminal targets
  dagger terminal go:dev             # Open the go:dev terminal target
  dagger tty go:dev                  # Use the short command alias
`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTerminalCommand,
}

func runTerminalCommand(cmd *cobra.Command, args []string) error {
	if !terminalListMode && len(args) == 0 {
		return fmt.Errorf("terminal target required; use 'dagger terminal -l' to list available targets")
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
			_, err := terminals.Run().ID(ctx)
			return err
		},
	)
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
	return writeCommandList(cmd.OutOrStdout(), items)
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
