package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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

// show only focused vertices. enabled by default for dagger do.
var focus bool

var interactive = os.Getenv("_EXPERIMENTAL_DAGGER_INTERACTIVE_TUI") != ""

type runSessionCallback func(context.Context, *client.Session) error

func withEngineAndTUI(
	ctx context.Context,
	params client.SessionParams,
	fn runSessionCallback,
) error {
	// TODO: update for workdir being gone
	/*
		if engineConf.Workdir == "" {
			engineConf.Workdir = workdir
		}
	*/

	if params.RunnerHost == "" {
		params.RunnerHost = engine.RunnerHost()
	}

	params.DisableHostRW = disableHostRW

	if params.JournalFile == "" {
		params.JournalFile = os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL")
	}

	if params.ExtraSearchDomains == nil {
		params.ExtraSearchDomains = strings.Fields(os.Getenv("_EXPERIMENTAL_DAGGER_SEARCH_DOMAIN"))
	}

	if !silent {
		if progress == "auto" && autoTTY || progress == "tty" {
			if interactive {
				return interactiveTUI(ctx, params, fn)
			}

			return inlineTUI(ctx, params, fn)
		}

		params.ProgrockWriter = console.NewWriter(os.Stderr, console.ShowInternal(debug))

		params.EngineNameCallback = func(name string) {
			fmt.Fprintln(os.Stderr, "Connected to engine", name)
		}

		params.CloudURLCallback = func(cloudURL string) {
			fmt.Fprintln(os.Stderr, "Dagger Cloud URL:", cloudURL)
		}
	}

	sess, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	defer sess.Close()
	return fn(ctx, sess)
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
	params client.SessionParams,
	fn runSessionCallback,
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

	sess, err := client.Connect(ctx, params)
	if err != nil {
		tuiErr := <-tuiDone
		return errors.Join(tuiErr, err)
	}
	defer sess.Close()

	err = fn(ctx, sess)
	tuiErr := <-tuiDone
	return errors.Join(tuiErr, err)
}

func inlineTUI(
	ctx context.Context,
	params client.SessionParams,
	fn runSessionCallback,
) error {
	tape := progrock.NewTape()
	tape.ShowInternal(debug)
	tape.Focus(focus)

	progW, engineErr := progrockTee(tape)
	if engineErr != nil {
		return engineErr
	}

	params.ProgrockWriter = progW

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	program, stop := progrock.DefaultUI().RenderLoop(quit, tape, os.Stderr, true)
	defer stop()

	params.CloudURLCallback = func(cloudURL string) {
		program.Send(progrock.StatusInfoMsg{
			Name:  "Cloud URL",
			Value: cloudURL,
			Order: 1,
		})
	}

	params.EngineNameCallback = func(name string) {
		program.Send(progrock.StatusInfoMsg{
			Name:  "Engine",
			Value: name,
			Order: 2,
		})
	}

	sess, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	defer sess.Close()

	before := time.Now()
	defer func() {
		program.Send(progrock.StatusInfoMsg{
			Name:  "Duration",
			Value: time.Since(before).Truncate(time.Millisecond).String(),
			Order: 3,
		})
	}()

	return fn(ctx, sess)
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
