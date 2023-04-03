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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/internal/engine/journal"
	"github.com/dagger/dagger/internal/tui"
	"github.com/dagger/dagger/router"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:                   "run [command]",
	Aliases:               []string{"r"},
	DisableFlagsInUseLine: true,
	Long: `Runs the specified command in a Dagger session and shows progress in a TUI

DAGGER_SESSION_PORT and DAGGER_SESSION_TOKEN will be convieniently injected automatically.`,
	Short: "Runs a command in a Dagger session",
	Example: `  Run a Dagger pipeline written in Go:
    dagger run go run main.go

  Run a Dagger pipeline written in Python:
    dagger run node pipeline.mjs

  Run a Dagger API request directly:
    jq -n '{query:"{container{id}}"}' | \
      dagger run sh -c 'curl -s \
        -u $DAGGER_SESSION_TOKEN: \
        -H "content-type:application/json" \
        -d @- \
        http://127.0.0.1:$DAGGER_SESSION_PORT/query'`,
	Run:          Run,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
}

func init() {
	// don't require -- to disambiguate subcommand flags
	runCmd.Flags().SetInterspersed(false)
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
	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}

	sessionL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("session listen: %w", err)
	}
	defer sessionL.Close()

	journalR, journalW := journal.Pipe()

	sessionPort := fmt.Sprintf("%d", sessionL.Addr().(*net.TCPAddr).Port)
	os.Setenv("DAGGER_SESSION_PORT", sessionPort)
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	subCmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec

	// NB: go run lets its child process roam free when you interrupt it, so
	// make sure they all get signalled. (you don't normally notice this in a
	// shell because Ctrl+C sends to the process group.)
	ensureChildProcessesAreKilled(subCmd)

	cmdline := strings.Join(args, " ")
	model := tui.New(quit, journalR, cmdline)
	program := tea.NewProgram(model, tea.WithAltScreen())
	subCmd.Stdin = os.Stdin
	subCmd.Stdout = progOutWriter{program}
	subCmd.Stderr = progOutWriter{program}

	exited := make(chan error, 1)

	var finalModel tui.Model
	err = withEngine(ctx, sessionToken.String(), journalW, progOutWriter{program}, func(ctx context.Context, api *router.Router) error {
		go http.Serve(sessionL, api) // nolint:gosec

		err := subCmd.Start()
		if err != nil {
			return err
		}

		go func() {
			exitErr := subCmd.Wait()
			exited <- exitErr
			program.Send(tui.CommandExitMsg{Err: exitErr})
		}()

		m, err := program.Run()
		finalModel = m.(tui.Model)
		return err
	})

	if finalModel.IsDone() {
		// propagate command result
		return <-exited
	}

	// something else happened; bubble up error, if any
	return err
}

type progOutWriter struct {
	prog *tea.Program
}

func (w progOutWriter) Write(p []byte) (int, error) {
	w.prog.Send(tui.CommandOutMsg{Output: p})
	return len(p), nil
}
