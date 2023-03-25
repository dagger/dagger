package main

import (
	"context"
	"io"
	"os"

	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/engine/journal"
	"github.com/dagger/dagger/router"
)

func withEngine(
	ctx context.Context,
	sessionToken string,
	journalW journal.Writer,
	logsW io.Writer,
	cb engine.StartCallback,
) error {
	engineConf := &engine.Config{
		Workdir:       workdir,
		ConfigPath:    configPath,
		SessionToken:  sessionToken,
		RunnerHost:    internalengine.RunnerHost(),
		DisableHostRW: disableHostRW,
		JournalURI:    os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"),
		JournalWriter: journalW,
		LogOutput:     logsW,
	}
	if debugLogs {
		engineConf.LogOutput = logsW
	}
	return engine.Start(ctx, engineConf, func(ctx context.Context, r *router.Router) error {
		return cb(ctx, r)
	})
}
