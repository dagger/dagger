package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/engine/journal"
	"github.com/dagger/dagger/internal/tui"
	"github.com/dagger/dagger/router"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
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

  Run a Dagger pipeline written in Node.js:
    dagger run node index.mjs

  Run a Dagger pipeline written in Python:
    dagger run python main.py

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

var waitDelay time.Duration
var useShinyNewTUI = os.Getenv("_EXPERIMENTAL_DAGGER_TUI") != ""
var interactive bool

func init() {
	// don't require -- to disambiguate subcommand flags
	runCmd.Flags().SetInterspersed(false)

	runCmd.Flags().DurationVar(
		&waitDelay,
		"cleanup-timeout",
		10*time.Second,
		"max duration to wait between SIGTERM and SIGKILL on interrupt",
	)

	runCmd.Flags().BoolVarP(
		&interactive,
		"interactive",
		"i",
		false,
		"use interactive tree-style TUI",
	)
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

	sessionPort := fmt.Sprintf("%d", sessionL.Addr().(*net.TCPAddr).Port)
	os.Setenv("DAGGER_SESSION_PORT", sessionPort)
	os.Setenv("DAGGER_SESSION_TOKEN", sessionToken.String())

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	subCmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec

	// allow piping to the command
	subCmd.Stdin = os.Stdin

	// NB: go run lets its child process roam free when you interrupt it, so
	// make sure they all get signalled. (you don't normally notice this in a
	// shell because Ctrl+C sends to the process group.)
	ensureChildProcessesAreKilled(subCmd)

	if !useShinyNewTUI {
		subCmd.Stdin = os.Stdin
		subCmd.Stdout = os.Stdout
		subCmd.Stderr = os.Stderr

		return withEngine(ctx, sessionToken.String(), nil, os.Stderr, func(ctx context.Context, api *router.Router) error {
			go http.Serve(sessionL, api) // nolint:gosec
			return subCmd.Run()
		})
	}

	if interactive {
		return interactiveTUI(ctx, sessionToken, sessionL, subCmd, quit)
	}

	return inlineTUI(ctx, sessionToken, sessionL, subCmd, quit)
}

func interactiveTUI(
	ctx context.Context,
	sessionToken uuid.UUID,
	sessionL net.Listener,
	subCmd *exec.Cmd,
	quit func(),
) error {
	journalR, journalW := journal.Pipe()

	cmdline := strings.Join(subCmd.Args, " ")
	program := tea.NewProgram(tui.New(quit, journalR, cmdline), tea.WithAltScreen())

	subCmd.Stdout = progOutWriter{program}
	subCmd.Stderr = progOutWriter{program}

	tuiErrs := make(chan error, 1)

	var finalModel tui.Model
	go func() {
		m, err := program.Run()
		finalModel = m.(tui.Model)
		tuiErrs <- err
	}()

	err := withEngine(ctx, sessionToken.String(), journalW, progOutWriter{program}, func(ctx context.Context, api *router.Router) error {
		go http.Serve(sessionL, api) // nolint:gosec

		cmdErr := subCmd.Run()
		program.Send(tui.CommandExitMsg{Err: cmdErr})
		return cmdErr
	})

	tuiErr := <-tuiErrs
	if finalModel.IsDone() {
		return err
	}

	return errors.Join(tuiErr, err)
}

func inlineTUI(
	ctx context.Context,
	sessionToken uuid.UUID,
	sessionL net.Listener,
	subCmd *exec.Cmd,
	quit func(),
) error {
	tape := progrock.NewTape()
	if debugLogs {
		tape.ShowInternal(true)
	}

	mw := progrock.MultiWriter{tape}
	if log := os.Getenv("_EXPERIMENTAL_DAGGER_PROGROCK_JOURNAL"); log != "" {
		w, err := newProgrockWriter(log)
		if err != nil {
			return fmt.Errorf("open progrock log: %w", err)
		}

		mw = append(mw, w)
	}

	stop := progrock.DefaultUI().RenderLoop(quit, tape, os.Stderr, true)
	defer stop()

	cmdline := strings.Join(subCmd.Args, " ")

	engineConf := engine.Config{
		Workdir:        workdir,
		ConfigPath:     configPath,
		SessionToken:   sessionToken.String(),
		RunnerHost:     internalengine.RunnerHost(),
		DisableHostRW:  disableHostRW,
		JournalURI:     os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"),
		ProgrockWriter: mw,
	}

	var cmdErr error
	err := engine.Start(ctx, engineConf, func(ctx context.Context, api *router.Router) error {
		rec := progrock.RecorderFromContext(ctx)

		go http.Serve(sessionL, api) // nolint:gosec

		cmdVtx := rec.Vertex("cmd", cmdline)
		subCmd.Stdout = cmdVtx.Stdout()
		subCmd.Stderr = cmdVtx.Stderr()

		cmdErr = subCmd.Run()
		cmdVtx.Done(cmdErr)
		return nil
	})
	if cmdErr != nil {
		return cmdErr
	}

	return err
}

type progOutWriter struct {
	prog *tea.Program
}

func (w progOutWriter) Write(p []byte) (int, error) {
	w.prog.Send(tui.CommandOutMsg{Output: p})
	return len(p), nil
}

func newProgrockWriter(dest string) (progrock.Writer, error) {
	f, err := os.Create(dest)
	if err != nil {
		return nil, err
	}

	return progrockFileWriter{
		enc: json.NewEncoder(f),
		c:   f,
	}, nil
}

type progrockFileWriter struct {
	enc *json.Encoder
	c   io.Closer
}

var _ progrock.Writer = progrockFileWriter{}

func (p progrockFileWriter) WriteStatus(update *progrock.StatusUpdate) error {
	return p.enc.Encode(update)
}

func (p progrockFileWriter) Close() error {
	return p.c.Close()
}
