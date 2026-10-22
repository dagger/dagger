package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"

	"github.com/dagger/dagger/engine/config"
)

func TestGracefulStopTimeoutSetting(t *testing.T) {
	setting := func(t *testing.T, cfg config.Config, args ...string) (time.Duration, error) {
		t.Helper()
		app := cli.NewApp()
		addFlags(app)
		var (
			got    time.Duration
			gotErr error
		)
		app.Action = func(c *cli.Context) error {
			got, gotErr = gracefulStopTimeoutSetting(c, cfg)
			return nil
		}
		require.NoError(t, app.Run(append([]string{"dagger-engine"}, args...)))
		return got, gotErr
	}
	withConfig := func(d time.Duration) config.Config {
		var cfg config.Config
		cfg.Shutdown.GracefulStopTimeout.Duration = d
		return cfg
	}

	t.Run("default", func(t *testing.T) {
		got, err := setting(t, config.Config{})
		require.NoError(t, err)
		require.Equal(t, 5*time.Minute, got)
	})
	t.Run("config", func(t *testing.T) {
		got, err := setting(t, withConfig(50*time.Second))
		require.NoError(t, err)
		require.Equal(t, 50*time.Second, got)
	})
	t.Run("flag wins over config", func(t *testing.T) {
		got, err := setting(t, withConfig(50*time.Second), "--graceful-stop-timeout", "40s")
		require.NoError(t, err)
		require.Equal(t, 40*time.Second, got)
	})
	t.Run("flag must be positive", func(t *testing.T) {
		_, err := setting(t, config.Config{}, "--graceful-stop-timeout", "0s")
		require.Error(t, err)
	})
	t.Run("config must not be negative", func(t *testing.T) {
		_, err := setting(t, withConfig(-time.Second))
		require.Error(t, err)
	})
}

func TestStopBudget(t *testing.T) {
	t.Run("the server gets the budget from the first signal", func(t *testing.T) {
		now := time.Unix(1000, 0)
		b := newStopBudget(func() time.Time { return now })
		b.setTimeout(50 * time.Second)
		b.start()
		now = now.Add(5 * time.Second)
		b.start() // a later signal doesn't move the start

		ctx, cancel := b.serverContext(context.Background())
		defer cancel()
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Equal(t, time.Unix(1050, 0), deadline)
	})

	t.Run("a flush is capped, and gets only what remains", func(t *testing.T) {
		now := time.Unix(1000, 0)
		b := newStopBudget(func() time.Time { return now })
		b.setTimeout(50 * time.Second)
		b.start()

		now = time.Unix(1010, 0)
		ctx, cancel := b.flushContext(context.Background())
		deadline, ok := ctx.Deadline()
		cancel()
		require.True(t, ok)
		require.Equal(t, time.Unix(1040, 0), deadline, "plenty of budget left: capped at the flush timeout")

		now = time.Unix(1040, 0)
		ctx, cancel = b.flushContext(context.Background())
		deadline, ok = ctx.Deadline()
		cancel()
		require.True(t, ok)
		require.Equal(t, time.Unix(1050, 0), deadline, "near the end: only the remainder")

		now = time.Unix(1070, 0)
		ctx, cancel = b.flushContext(context.Background())
		deadline, ok = ctx.Deadline()
		cancel()
		require.True(t, ok)
		require.Equal(t, time.Unix(1050, 0), deadline, "after the budget: no time at all")
	})

	t.Run("a stop that starts without a signal starts the budget itself", func(t *testing.T) {
		now := time.Unix(1000, 0)
		b := newStopBudget(func() time.Time { return now })
		b.setTimeout(time.Minute)

		ctx, cancel := b.serverContext(context.Background())
		defer cancel()
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Equal(t, time.Unix(1060, 0), deadline)
	})

	t.Run("the budget's contexts outlive the cancelled run context", func(t *testing.T) {
		b := newStopBudget(time.Now)
		b.setTimeout(time.Hour)
		b.start()
		parent, cancelParent := context.WithCancel(context.Background())
		cancelParent()

		ctx, cancel := b.serverContext(parent)
		defer cancel()
		require.NoError(t, ctx.Err())
		_, ok := ctx.Deadline()
		require.True(t, ok)

		fctx, fcancel := b.flushContext(parent)
		defer fcancel()
		require.NoError(t, fctx.Err())
	})
}
