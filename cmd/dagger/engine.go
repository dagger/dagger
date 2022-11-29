package main

import (
	"context"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
)

func withEngine(
	ctx context.Context,
	sessionToken string,
	allowedLocalDirs []string,
	cb engine.StartCallback,
) error {
	var runnerHost string
	if v, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"); ok {
		runnerHost = v
	} else {
		runnerHost = "docker-image://" + engineImageRef()
	}

	engineConf := &engine.Config{
		Workdir:          workdir,
		ConfigPath:       configPath,
		SessionToken:     sessionToken,
		RunnerHost:       runnerHost,
		AllowedLocalDirs: allowedLocalDirs,
	}
	if debugLogs {
		engineConf.LogOutput = os.Stderr
	}
	return engine.Start(ctx, engineConf, func(ctx context.Context, r *router.Router) error {
		return cb(ctx, r)
	})
}
