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

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/ioctx"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/telemetry"
)

var runCmd = &cobra.Command{
	Use:     "run [OPTIONS] COMMAND",
	Aliases: []string{"r"},
	Short:   "Run a command in a Dagger session",
	Long: strings.ReplaceAll(
		`Executes the specified command in a Dagger Session and displays
live progress in a TUI.

´DAGGER_SESSION_PORT´ and ´DAGGER_SESSION_TOKEN´ will be conveniently
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
	ctx := cmd.Context()

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

	return withEngine(ctx, client.Params{
		SecretToken: sessionToken,
	}, func(ctx context.Context, engineClient *client.Client) error {
		sessionL, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("session listen: %w", err)
		}
		defer sessionL.Close()

		env := os.Environ()
		sessionPort := fmt.Sprintf("%d", sessionL.Addr().(*net.TCPAddr).Port)
		env = append(env, "DAGGER_SESSION_PORT="+sessionPort)
		env = append(env, "DAGGER_SESSION_TOKEN="+sessionToken)
		env = append(env, telemetry.PropagationEnv(ctx)...)

		subCmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec

		subCmd.Env = env

		// allow piping to the command
		subCmd.Stdin = os.Stdin

		// NB: go run lets its child process roam free when you interrupt it, so
		// make sure they all get signalled. (you don't normally notice this in a
		// shell because Ctrl+C sends to the process group.)
		ensureChildProcessesAreKilled(subCmd)

		srv := &http.Server{ //nolint:gosec
			Handler: engineClient,
			BaseContext: func(listener net.Listener) context.Context {
				return ctx
			},
		}

		go srv.Serve(sessionL)

		var cmdErr error
		if !silent {
			if stdoutIsTTY {
				subCmd.Stdout = ioctx.Stdout(ctx)
			} else {
				subCmd.Stdout = os.Stdout
			}

			if stderrIsTTY {
				subCmd.Stderr = ioctx.Stderr(ctx)
			} else {
				subCmd.Stderr = os.Stderr
			}

			cmdErr = subCmd.Run()
		} else {
			subCmd.Stdout = os.Stdout
			subCmd.Stderr = os.Stderr
			cmdErr = subCmd.Run()
		}

		return cmdErr
	})
}
