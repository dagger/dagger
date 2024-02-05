package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/dagql/ioctx"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tui"
	"github.com/mattn/go-isatty"
	"github.com/vito/progrock"
	"github.com/vito/progrock/console"
)

var silent bool

var progress string
var stdoutIsTTY = isatty.IsTerminal(os.Stdout.Fd())
var stderrIsTTY = isatty.IsTerminal(os.Stderr.Fd())

var autoTTY = stdoutIsTTY || stderrIsTTY

func init() {
	rootCmd.PersistentFlags().BoolVarP(
		&silent,
		"silent",
		"s",
		false,
		"disable terminal UI and progress output",
	)

	rootCmd.PersistentFlags().StringVar(
		&progress,
		"progress",
		"auto",
		"progress output format (auto, plain, tty)",
	)
}

// show only focused vertices
var focus bool

// show errored vertices even if focused
//
// set this to false if your command handles errors (e.g. dagger checks)
var revealErrored = true

var interactive = os.Getenv("_EXPERIMENTAL_DAGGER_INTERACTIVE_TUI") != ""

type runClientCallback func(context.Context, *client.Client) error

func withEngineAndTUI(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	if params.RunnerHost == "" {
		params.RunnerHost = engine.RunnerHost()
	}

	params.DisableHostRW = disableHostRW

	if params.JournalFile == "" {
		params.JournalFile = os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL")
	}

	if !silent {
		if progress == "auto" && autoTTY || progress == "tty" {
			if interactive {
				return interactiveTUI(ctx, params, fn)
			}

			return inlineTUI(ctx, params, fn)
		}

		opts := []console.WriterOpt{
			console.ShowInternal(debug),
		}
		if debug {
			opts = append(opts, console.WithMessageLevel(progrock.MessageLevel_DEBUG))
		}

		progW := console.NewWriter(os.Stderr, opts...)
		params.ProgrockWriter = progW
		params.EngineNameCallback = func(name string) {
			fmt.Fprintln(os.Stderr, "Connected to engine", name)
		}
		params.CloudURLCallback = func(cloudURL string) {
			fmt.Fprintln(os.Stderr, "Dagger Cloud URL:", cloudURL)
		}
	}

	engineClient, ctx, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	defer engineClient.Close()
	return fn(ctx, engineClient)
}

func interactiveTUI(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	progR, progW := progrock.Pipe()
	params.ProgrockWriter = progW

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	program := tea.NewProgram(tui.New(quit, progR), tea.WithAltScreen())

	tuiDone := make(chan error, 1)
	go func() {
		_, err := program.Run()
		tuiDone <- err
	}()

	sess, ctx, err := client.Connect(ctx, params)
	if err != nil {
		tuiErr := <-tuiDone
		return errors.Join(tuiErr, err)
	}

	err = fn(ctx, sess)

	closeErr := sess.Close()

	tuiErr := <-tuiDone
	return errors.Join(tuiErr, closeErr, err)
}

func inlineTUI(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	frontend := idtui.New()
	frontend.Debug = debug
	params.ProgrockWriter = frontend
	outBuf := new(bytes.Buffer)
	ctx = ioctx.WithStdout(ctx, outBuf)
	errBuf := new(bytes.Buffer)
	ctx = ioctx.WithStderr(ctx, errBuf)
	err := frontend.Run(ctx, func(ctx context.Context) error {
		sess, ctx, err := client.Connect(ctx, params)
		if err != nil {
			return err
		}
		defer sess.Close()
		return fn(ctx, sess)
	})
	fmt.Fprint(os.Stdout, outBuf.String())
	fmt.Fprint(os.Stderr, errBuf.String())
	return err
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
