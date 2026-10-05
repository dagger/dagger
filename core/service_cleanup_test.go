package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine/engineutil"
	gatewayapi "github.com/dagger/dagger/internal/buildkit/frontend/gateway/pb"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestContainerServiceStopCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		exited := make(chan struct{})
		cleanupCause := errors.New("runc delete: cgroup busy")
		var cleanupErr error
		running := &RunningService{Stop: func(ctx context.Context, _ bool) error {
			_, err := waitContainerServiceStop(ctx, exited, &cleanupErr)
			return err
		}}
		result := make(chan error, 1)
		go func() { result <- running.Stop(ctx, false) }()
		synctest.Wait()
		cleanupErr = &engineutil.ExecCleanupError{Err: cleanupCause}
		close(exited)
		err := <-result
		require.ErrorIs(t, err, cleanupCause, "Stop must not report success after cleanup failure")
		var typed *engineutil.ExecCleanupError
		require.ErrorAs(t, err, &typed)
	})
}

func TestContainerServiceStopResult(t *testing.T) {
	t.Run("normal exit has no cleanup error", func(t *testing.T) {
		exited := make(chan struct{})
		close(exited)
		var cleanupErr error
		finished, err := waitContainerServiceStop(t.Context(), exited, &cleanupErr)
		require.True(t, finished)
		require.NoError(t, err)
	})
	t.Run("cancellation before exit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var cleanupErr error
		finished, err := waitContainerServiceStop(ctx, make(chan struct{}), &cleanupErr)
		require.False(t, finished)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestContainerServiceCleanupPublication(t *testing.T) {
	for _, mode := range []string{"signal only", "signal and executor cleanup", "signal and local cleanup", "joined wrapped cleanup"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				signalErr := &gatewayapi.ExitError{ExitCode: 137}
				first, second := errors.New("delete failed"), errors.New("release failed")
				var runErr error = signalErr
				var localErr error
				var expected []error
				switch mode {
				case "signal and executor cleanup":
					runErr = errors.Join(signalErr, &engineutil.ExecCleanupError{Err: first})
					expected = []error{first}
				case "signal and local cleanup":
					localErr = first
					expected = []error{first}
				case "joined wrapped cleanup":
					runErr = fmt.Errorf("runtime: %w", errors.Join(signalErr, &engineutil.ExecCleanupError{Err: first}, fmt.Errorf("secondary: %w", &engineutil.ExecCleanupError{Err: second})))
					expected = []error{first, second}
				}
				origin := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
				exited := make(chan struct{})
				var exitErr, cleanupErr error
				running := &RunningService{
					Stop: func(ctx context.Context, _ bool) error {
						_, err := waitContainerServiceStop(ctx, exited, &cleanupErr)
						return err
					},
					Wait: func(ctx context.Context) error {
						_, err := waitContainerServiceResult(ctx, exited, func() error { return exitErr })
						return err
					},
				}
				stops := make(chan error, 2)
				waits := make(chan error, 1)
				go func() { stops <- running.Stop(ctx, false) }()
				go func() { stops <- running.Stop(ctx, true) }()
				go func() { waits <- running.Wait(ctx) }()
				synctest.Wait()
				// This is the same classification and publication-before-close boundary
				// used after the real executor and service cleanups return.
				exitErr, cleanupErr = classifyContainerServiceExit(runErr, localErr, origin)
				close(exited)
				waitErr := <-waits
				require.ErrorIs(t, waitErr, signalErr)
				require.NotEmpty(t, telemetry.ParseErrorOrigins(waitErr.Error()))
				for range 2 {
					stopErr := <-stops
					if len(expected) == 0 {
						require.NoError(t, stopErr)
					} else {
						require.NotEmpty(t, telemetry.ParseErrorOrigins(stopErr.Error()))
					}
					require.NotErrorIs(t, stopErr, signalErr, "Stop suppresses expected signal exit only")
					for _, cause := range expected {
						require.ErrorIs(t, stopErr, cause)
						require.ErrorIs(t, waitErr, cause)
					}
				}
			})
		})
	}
}
