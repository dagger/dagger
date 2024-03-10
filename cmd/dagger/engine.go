package main

import (
	"context"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/tracing"
	"github.com/mattn/go-isatty"
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

type runClientCallback func(context.Context, *client.Client) error

func withEngine(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	if params.RunnerHost == "" {
		var err error
		params.RunnerHost, err = engine.RunnerHost()
		if err != nil {
			return err
		}
	}

	params.DisableHostRW = disableHostRW

	params.EngineNameCallback = Frontend.ConnectedToEngine

	params.CloudURLCallback = Frontend.ConnectedToCloud

	params.EngineTrace = Frontend
	params.EngineLogs = Frontend

	if exp, ok := tracing.ConfiguredSpanExporter(ctx); ok {
		if !tracing.ForceLiveTrace {
			exp = tracing.FilterLiveSpansExporter{
				// SpanProcessor: processor,
				SpanExporter: exp,
			}
		}
		params.EngineTrace = tracing.MultiSpanExporter{
			params.EngineTrace,
			exp,
		}
	}

	if exp, ok := tracing.ConfiguredLogExporter(ctx); ok {
		params.EngineLogs = tracing.MultiLogExporter{
			params.EngineLogs,
			exp,
		}
	}

	sess, ctx, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	err = fn(ctx, sess)
	sess.Close(err)
	return err
}
