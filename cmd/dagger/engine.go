package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
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

		params.ProgrockWriter = console.NewWriter(os.Stderr, opts...)

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
	params client.Params,
	fn runClientCallback,
) error {
	progR, progW := progrock.Pipe()
	progW, err := progrockTee(progW)
	if err != nil {
		return err
	}

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
	tape := progrock.NewTape()
	tape.ShowInternal(debug)
	tape.Focus(focus)
	tape.RevealErrored(revealErrored)

	if os.Getenv("IDS") != "" {
		tape.RenderIDs(true)
	}

	if debug {
		tape.MessageLevel(progrock.MessageLevel_DEBUG)
	}

	progW, engineErr := progrockTee(tape)
	if engineErr != nil {
		return engineErr
	}

	params.ProgrockWriter = progW

	return progrock.DefaultUI().Run(ctx, tape, func(ctx context.Context, ui progrock.UIClient) error {
		params.CloudURLCallback = func(cloudURL string) {
			ui.SetStatusInfo(progrock.StatusInfo{
				Name:  "Cloud URL",
				Value: cloudURL,
				Order: 1,
			})
		}

		params.EngineNameCallback = func(name string) {
			ui.SetStatusInfo(progrock.StatusInfo{
				Name:  "Engine",
				Value: name,
				Order: 2,
			})
		}

		sess, ctx, err := client.Connect(ctx, params)
		if err != nil {
			return err
		}
		defer sess.Close()
		return fn(ctx, sess)
	})
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
