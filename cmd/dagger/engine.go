package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/tui"
	"github.com/dagger/dagger/router"
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

	if engineConf.BuildkitJournalFile == "" {
		engineConf.BuildkitJournalFile = os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL")
	}

	if engineConf.ProgrockJournalFile == "" {
		engineConf.ProgrockJournalFile = os.Getenv("_EXPERIMENTAL_DAGGER_PROGROCK_JOURNAL")
	}

	if !silent {
		if progress == "auto" && autoTTY || progress == "tty" {
			if interactive {
				return interactiveTUI(ctx, engineConf, fn)
			}

			return inlineTUI(ctx, engineConf, fn)
		}

		engineConf.ProgrockWriter = console.NewWriter(os.Stderr, console.ShowInternal(debug))

		engineConf.EngineNameCallback = func(name string) {
			fmt.Fprintln(os.Stderr, "Connected to engine", name)
		}

		engineConf.CloudURLCallback = func(cloudURL string) {
			fmt.Fprintln(os.Stderr, "Dagger Cloud URL:", cloudURL)
		}
	}

	return engine.Start(ctx, engineConf, fn)
}

func interactiveTUI(
	ctx context.Context,
	engineConf engine.Config,
	fn engine.StartCallback,
) error {
	progR, progW := progrock.Pipe()

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

	tuiErr := <-tuiDone

	if cbErr != nil {
		// avoid unnecessary error wrapping
		return cbErr
	}

	return errors.Join(tuiErr, engineErr)
}

func inlineTUI(
	ctx context.Context,
	engineConf engine.Config,
	fn engine.StartCallback,
) error {
	tape := progrock.NewTape()
	tape.ShowInternal(debug)
	tape.Focus(focus)

	engineConf.ProgrockWriter = tape

	ctx, quit := context.WithCancel(ctx)
	defer quit()

	program, stop := progrock.DefaultUI().RenderLoop(quit, tape, os.Stderr, true)
	defer stop()

	engineConf.CloudURLCallback = func(cloudURL string) {
		program.Send(progrock.StatusInfoMsg{
			Name:  "Cloud URL",
			Value: cloudURL,
			Order: 1,
		})
	}

	engineConf.EngineNameCallback = func(name string) {
		program.Send(progrock.StatusInfoMsg{
			Name:  "Engine",
			Value: name,
			Order: 2,
		})
	}

	var cbErr error
	engineErr := engine.Start(ctx, engineConf, func(ctx context.Context, api *router.Router) error {
		before := time.Now()

		cbErr = fn(ctx, api)

		program.Send(progrock.StatusInfoMsg{
			Name:  "Duration",
			Value: time.Since(before).Truncate(time.Millisecond).String(),
			Order: 3,
		})

		return cbErr
	})

	if cbErr != nil {
		return cbErr
	} else if engineErr != nil {
		return engineErr
	}

	return nil
}
