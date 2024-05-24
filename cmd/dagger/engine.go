package main

import (
	"context"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/telemetry"
)

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

	params.EngineTrace = telemetry.SpanForwarder{
		Processors: telemetry.SpanProcessors,
	}
	params.EngineLogs = telemetry.LogForwarder{
		Processors: telemetry.LogProcessors,
	}

	sess, ctx, err := client.Connect(ctx, params)
	if err != nil {
		return err
	}
	defer sess.Close()

	return fn(ctx, sess)
}
