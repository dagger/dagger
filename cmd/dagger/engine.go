package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/idtui"
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

var useLegacyTUI = os.Getenv("_EXPERIMENTAL_DAGGER_LEGACY_TUI") != ""

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

	if interactive {
		return interactiveTUI(ctx, params, fn)
	}
	if useLegacyTUI {
		if progress == "auto" && autoTTY || progress == "tty" {
			return legacyTUI(ctx, params, fn)
		} else {
			return plainConsole(ctx, params, fn)
		}
	}
	return runWithFrontend(ctx, params, fn)
}

// TODO remove when legacy TUI is no longer supported; this has been
// assimilated into idtui.Frontend
func plainConsole(ctx context.Context, params client.Params, fn runClientCallback) error {
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
	engineClient, ctx, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	defer engineClient.Close()
	return fn(ctx, engineClient)
}

func runWithFrontend(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	frontend := idtui.New()
	frontend.Debug = debug
	frontend.Plain = progress == "plain"
	frontend.Silent = silent
	params.ProgrockWriter = frontend
	params.EngineNameCallback = frontend.ConnectedToEngine
	params.CloudURLCallback = frontend.ConnectedToCloud
	return frontend.Run(ctx, func(ctx context.Context) error {
		sess, ctx, err := client.Connect(ctx, params)
		if err != nil {
			return err
		}
		defer sess.Close()
		return fn(ctx, sess)
	})
}

func legacyTUI(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	tape := progrock.NewTape()
	tape.ShowInternal(debug)
	tape.Focus(focus)
	tape.RevealErrored(revealErrored)

	if debug {
		tape.MessageLevel(progrock.MessageLevel_DEBUG)
	}

	params.ProgrockWriter = tape

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
