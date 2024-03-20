package main

import (
	"context"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/telemetry"
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

	if exp, ok := telemetry.ConfiguredSpanExporter(ctx); ok {
		if !telemetry.ForceLiveTrace {
			exp = telemetry.FilterLiveSpansExporter{
				// SpanProcessor: processor,
				SpanExporter: exp,
			}
		}
		params.EngineTrace = telemetry.MultiSpanExporter{params.EngineTrace, exp}
	}

	if exp, ok := telemetry.ConfiguredLogExporter(ctx); ok {
		params.EngineLogs = telemetry.MultiLogExporter{params.EngineLogs, exp}
	}

	if spans, logs, ok := telemetry.ConfiguredCloudExporters(ctx); ok {
		params.EngineTrace = telemetry.MultiSpanExporter{params.EngineTrace, spans}
		params.EngineLogs = telemetry.MultiLogExporter{params.EngineLogs, logs}
	}

	sess, ctx, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	defer sess.Close()

	return fn(ctx, sess)
}
