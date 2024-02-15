package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
)

var runCmd = &cobra.Command{
	Use:     "run [flags] COMMAND",
	Aliases: []string{"r"},
	Short:   "Run a command in a Dagger session",
	Long: strings.ReplaceAll(
		`Executes the specified command in a Dagger Session and displays
live progress in a TUI.

´DAGGER_SESSION_PORT´ and ´DAGGER_SESSION_TOKEN´ will be convieniently
injected automatically.

For example:
´´´shell
jq -n '{query:"{container{id}}"}' | \
  dagger run sh -c 'curl -s \
    -u $DAGGER_SESSION_TOKEN: \
    -H "content-type:application/json" \
    -d @- \
    http://127.0.0.1:$DAGGER_SESSION_PORT/query
´´´`,
		"´",
		"`",
	),
	Example: strings.TrimSpace(`
dagger run go run main.go
dagger run node index.mjs
dagger run python main.py
`,
	),
	GroupID:      execGroup.ID,
	Run:          Run,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
}

var waitDelay time.Duration
var runFocus bool

func init() {
	// don't require -- to disambiguate subcommand flags
	runCmd.Flags().SetInterspersed(false)

	runCmd.Flags().DurationVar(
		&waitDelay,
		"cleanup-timeout",
		10*time.Second,
		"max duration to wait between SIGTERM and SIGKILL on interrupt",
	)

	runCmd.Flags().BoolVar(&runFocus, "focus", false, "Only show output for focused commands.")
}

func Run(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	err := run(ctx, args)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "run canceled")
			os.Exit(2)
			return
		}

		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
		return
	}
}

func run(ctx context.Context, args []string) error {
	u, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}

	sessionToken := u.String()

	focus = runFocus
	useLegacyTUI = true
	return withEngineAndTUI(ctx, client.Params{
		SecretToken: sessionToken,
	}, func(ctx context.Context, engineClient *client.Client) error {
		sessionL, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("session listen: %w", err)
		}
		defer sessionL.Close()

		sessionPort := fmt.Sprintf("%d", sessionL.Addr().(*net.TCPAddr).Port)
		os.Setenv("DAGGER_SESSION_PORT", sessionPort)
		os.Setenv("DAGGER_SESSION_TOKEN", sessionToken)

		subCmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec

		// allow piping to the command
		subCmd.Stdin = os.Stdin

		// NB: go run lets its child process roam free when you interrupt it, so
		// make sure they all get signalled. (you don't normally notice this in a
		// shell because Ctrl+C sends to the process group.)
		ensureChildProcessesAreKilled(subCmd)

		go http.Serve(sessionL, engineClient) //nolint:gosec

		var cmdErr error
		if !silent {
			rec := progrock.FromContext(ctx)

			cmdline := strings.Join(subCmd.Args, " ")
			cmdVtx := rec.Vertex(idtui.PrimaryVertex, cmdline)

			if stdoutIsTTY {
				subCmd.Stdout = cmdVtx.Stdout()
			} else {
				subCmd.Stdout = os.Stdout
			}

			if stderrIsTTY {
				subCmd.Stderr = cmdVtx.Stderr()
			} else {
				subCmd.Stderr = os.Stderr
			}

			cmdErr = subCmd.Run()
			cmdVtx.Done(cmdErr)
		} else {
			subCmd.Stdout = os.Stdout
			subCmd.Stderr = os.Stderr
			cmdErr = subCmd.Run()
		}

		return cmdErr
	})
}
