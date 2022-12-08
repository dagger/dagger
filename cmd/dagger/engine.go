package main

import (
	"context"
	"os"

	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
)

func withEngine(
	ctx context.Context,
	sessionToken string,
	cb engine.StartCallback,
) error {
	engineConf := &engine.Config{
		Workdir:      workdir,
		ConfigPath:   configPath,
		SessionToken: sessionToken,
		RunnerHost:   internalengine.RunnerHost(),
	}
	if debugLogs {
		engineConf.LogOutput = os.Stderr
	}
	return engine.Start(ctx, engineConf, func(ctx context.Context, r *router.Router) error {
		return cb(ctx, r)
	})
}
