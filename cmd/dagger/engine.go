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
	"github.com/google/uuid"
	"github.com/vito/progrock"
)

var useShinyNewTUI = os.Getenv("_EXPERIMENTAL_DAGGER_TUI") != ""
var interactive bool

func init() {
	rootCmd.Flags().BoolVarP(
		&interactive,
		"interactive",
		"i",
		false,
		"use interactive tree-style TUI",
	)
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
