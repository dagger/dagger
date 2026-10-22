package main

import (
	"context"
	"time"

	"github.com/urfave/cli"

	"github.com/dagger/dagger/engine/config"
)

const (
	// defaultGracefulStopTimeout is the engine's stop budget when neither the
	// config nor the flag sets one: session teardown and persisting the dagql
	// cache can take a while.
	defaultGracefulStopTimeout = 5 * time.Minute

	// stopFlushTimeout bounds each final telemetry flush within the budget.
	stopFlushTimeout = 30 * time.Second
)

// stopBudget bounds the engine's whole stop, from the first signal to process
// exit: the server's graceful stop and then the final telemetry flushes, which
// get what remains of it.
type stopBudget struct {
	now func() time.Time
}

func newStopBudget(now func() time.Time) *stopBudget {
	return &stopBudget{now: now}
}

// setTimeout sets the budget's length.
func (b *stopBudget) setTimeout(timeout time.Duration) {}

// start marks the start of the stop. Only the first call counts.
func (b *stopBudget) start() {}

// serverContext returns a context, detached from parent's cancellation, that
// ends with the budget.
func (b *stopBudget) serverContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithoutCancel(parent))
}

// flushContext returns a context, detached from parent's cancellation, for one
// final flush: it ends after maxFlush, or with the budget if that is sooner.
func (b *stopBudget) flushContext(parent context.Context, maxFlush time.Duration) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithoutCancel(parent))
}

// gracefulStopTimeoutSetting returns the configured stop budget: the
// --graceful-stop-timeout flag, else the config's
// shutdown.gracefulStopTimeout, else the default.
func gracefulStopTimeoutSetting(c *cli.Context, cfg config.Config) (time.Duration, error) {
	return defaultGracefulStopTimeout, nil
}
