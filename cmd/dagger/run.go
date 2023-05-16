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
	return withEngineAndTUI(ctx, func(ctx context.Context, api *router.Router, sessionToken string) error {
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

		go http.Serve(sessionL, api) // nolint:gosec

		var cmdErr error
		if useShinyNewTUI {
			rec := progrock.RecorderFromContext(ctx)

			cmdline := strings.Join(subCmd.Args, " ")
			cmdVtx := rec.Vertex("cmd", cmdline)
			subCmd.Stdout = cmdVtx.Stdout()
			subCmd.Stderr = cmdVtx.Stderr()

			cmdErr = subCmd.Run()
			cmdVtx.Done(cmdErr)
		} else {
			subCmd.Stdout = os.Stdout
			subCmd.Stderr = os.Stderr
			cmdErr = subCmd.Run()
		}

		go http.Serve(sessionL, api) // nolint:gosec

		return cmdErr
	})
}

func progrockTee(progW progrock.Writer) (progrock.Writer, error) {
	if log := os.Getenv("_EXPERIMENTAL_DAGGER_PROGROCK_JOURNAL"); log != "" {
		fileW, err := newProgrockWriter(log)
		if err != nil {
			return nil, fmt.Errorf("open progrock log: %w", err)
		}

		return progrock.MultiWriter{progW, fileW}, nil
	}

	return progW, nil
}

func interactiveTUI(
	ctx context.Context,
	engineConf engine.Config,
	fn EngineTUIFunc,
) error {
	progR, progW := progrock.Pipe()
	progW, err := progrockTee(progW)
	if err != nil {
		return err
	}

	engineConf.ProgrockWriter = progW

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	program := tea.NewProgram(tui.New(quit, progR), tea.WithAltScreen())

	tuiDone := make(chan error, 1)
	go func() {
		_, err := program.Run()
		tuiDone <- err
	}()

	var cbErr error
	engineErr := engine.Start(ctx, engineConf, func(ctx context.Context, api *router.Router) error {
		cbErr = fn(ctx, api, engineConf.SessionToken)
		return cbErr
	})
	if cbErr != nil {
		// avoid unnecessary error wrapping
		return cbErr
	}

	tuiErr := <-tuiDone
	return errors.Join(tuiErr, engineErr)
}

func inlineTUI(
	ctx context.Context,
	engineConf engine.Config,
	fn EngineTUIFunc,
) error {
	tape := progrock.NewTape()
	if debugLogs {
		tape.ShowInternal(true)
	}

	progW, engineErr := progrockTee(tape)
	if engineErr != nil {
		return engineErr
	}

	engineConf.ProgrockWriter = progW

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	stop := progrock.DefaultUI().RenderLoop(quit, tape, os.Stderr, true)
	defer stop()

	var cbErr error
	engineErr = engine.Start(ctx, engineConf, func(ctx context.Context, api *router.Router) error {
		cbErr = fn(ctx, api, engineConf.SessionToken)
		return cbErr
	})
	if cbErr != nil {
		return cbErr
	}

	return engineErr
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

type EngineTUIFunc func(
	ctx context.Context,
	api *router.Router,
	sessionToken string,
) error

func withEngineAndTUI(
	ctx context.Context,
	fn EngineTUIFunc,
) error {
	sessionToken, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}

	engineConf := engine.Config{
		Workdir:       workdir,
		SessionToken:  sessionToken.String(),
		RunnerHost:    internalengine.RunnerHost(),
		DisableHostRW: disableHostRW,
		JournalFile:   os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"),
	}

	if !useShinyNewTUI {
		return engine.Start(ctx, engineConf, func(ctx context.Context, api *router.Router) error {
			return fn(ctx, api, engineConf.SessionToken)
		})
	}

	if interactive {
		return interactiveTUI(ctx, engineConf, fn)
	}

	return inlineTUI(ctx, engineConf, fn)
}
