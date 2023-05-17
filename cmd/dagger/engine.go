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
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/tui"
	"github.com/dagger/dagger/router"
	"github.com/mattn/go-isatty"
	"github.com/vito/progrock"
)

var useTUI = isatty.IsTerminal(os.Stdout.Fd()) ||
	isatty.IsTerminal(os.Stderr.Fd())

var interactive = os.Getenv("_EXPERIMENTAL_DAGGER_INTERACTIVE_TUI") != ""

func withEngineAndTUI(
	ctx context.Context,
	engineConf engine.Config,
	fn engine.StartCallback,
) error {
	if engineConf.Workdir == "" {
		engineConf.Workdir = workdir
	}

	if engineConf.RunnerHost == "" {
		engineConf.RunnerHost = internalengine.RunnerHost()
	}

	engineConf.DisableHostRW = disableHostRW

	if engineConf.JournalFile == "" {
		engineConf.JournalFile = os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL")
	}

	if !useTUI {
		if engineConf.LogOutput == nil {
			engineConf.LogOutput = os.Stderr
		}

		return engine.Start(ctx, engineConf, fn)
	}

	if interactive {
		return interactiveTUI(ctx, engineConf, fn)
	}

	return inlineTUI(ctx, engineConf, fn)
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
	fn engine.StartCallback,
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
		cbErr = fn(ctx, api)
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
	fn engine.StartCallback,
) error {
	tape := progrock.NewTape()
	tape.ShowAllOutput(true)
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
		cbErr = fn(ctx, api)
		return cbErr
	})
	if cbErr != nil {
		return cbErr
	}

	return engineErr
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
