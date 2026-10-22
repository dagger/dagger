package main

import (
	"context"
	"fmt"
	"sync"
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

// engineStopBudget is the process's stop budget. The signal handler starts it,
// and the action sets its length once the config is loaded.
var engineStopBudget = newStopBudget(time.Now)

// stopBudget bounds the engine's whole stop, from the first signal to process
// exit: the server's graceful stop and then the final telemetry flushes, which
// get what remains of it.
type stopBudget struct {
	now func() time.Time

	mu      sync.Mutex
	timeout time.Duration
	started time.Time
}

func newStopBudget(now func() time.Time) *stopBudget {
	return &stopBudget{now: now, timeout: defaultGracefulStopTimeout}
}

// setTimeout sets the budget's length.
func (b *stopBudget) setTimeout(timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timeout = timeout
}

// start marks the start of the stop. Only the first call counts.
func (b *stopBudget) start() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.startLocked()
}

func (b *stopBudget) startLocked() {
	if b.started.IsZero() {
		b.started = b.now()
	}
}

// deadline is the end of the budget, starting it if nothing has yet.
func (b *stopBudget) deadline() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.startLocked()
	return b.started.Add(b.timeout)
}

// serverContext returns a context, detached from parent's cancellation, that
// ends with the budget.
func (b *stopBudget) serverContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.WithoutCancel(parent), b.deadline())
}

// flushContext returns a context, detached from parent's cancellation, for one
// final flush: it ends after stopFlushTimeout, or with the budget if that is
// sooner.
func (b *stopBudget) flushContext(parent context.Context) (context.Context, context.CancelFunc) {
	deadline := b.deadline()
	if capped := b.now().Add(stopFlushTimeout); capped.Before(deadline) {
		deadline = capped
	}
	return context.WithDeadline(context.WithoutCancel(parent), deadline)
}

// gracefulStopTimeoutSetting returns the configured stop budget: the
// --graceful-stop-timeout flag, else the config's
// shutdown.gracefulStopTimeout, else the default.
func gracefulStopTimeoutSetting(c *cli.Context, cfg config.Config) (time.Duration, error) {
	if c.IsSet("graceful-stop-timeout") {
		timeout := c.Duration("graceful-stop-timeout")
		if timeout <= 0 {
			return 0, fmt.Errorf("--graceful-stop-timeout must be positive, got %s", timeout)
		}
		return timeout, nil
	}
	timeout := cfg.Shutdown.GracefulStopTimeout.Duration
	switch {
	case timeout < 0:
		return 0, fmt.Errorf("shutdown.gracefulStopTimeout must not be negative, got %s", timeout)
	case timeout == 0:
		return defaultGracefulStopTimeout, nil
	}
	return timeout, nil
}
