package server

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type attachableHealthHarness struct {
	ticks    chan time.Time
	results  chan error
	timeouts chan time.Duration
	checked  chan struct{}
	done     chan struct{}
}

func newAttachableHealthHarness(t *testing.T) *attachableHealthHarness {
	t.Helper()
	harness := &attachableHealthHarness{
		ticks:    make(chan time.Time),
		results:  make(chan error, 8),
		timeouts: make(chan time.Duration, 8),
		checked:  make(chan struct{}, 8),
		done:     make(chan struct{}),
	}
	go func() {
		runAttachableHealthMonitor(t.Context(), attachableHealthMonitorConfig{
			ticks: harness.ticks,
			now:   func() time.Time { return time.Unix(0, 0) },
			withTimeout: func(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
				harness.timeouts <- timeout
				return context.WithCancel(ctx)
			},
			check: func(context.Context) error {
				return <-harness.results
			},
			testChecked: func() { harness.checked <- struct{}{} },
		})
		close(harness.done)
	}()
	return harness
}

func waitAttachableHealthTest[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func (harness *attachableHealthHarness) step(t *testing.T, result error) {
	t.Helper()
	harness.results <- result
	harness.ticks <- time.Time{}
	require.Equal(t, 30*time.Second, waitAttachableHealthTest(t, harness.timeouts, "health timeout"))
	waitAttachableHealthTest(t, harness.checked, "health check completion")
}

func requireHealthMonitorRunning(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("health monitor exited early")
	default:
	}
}

func TestAttachableHealthSecondFailureIsFatal(t *testing.T) {
	t.Parallel()
	harness := newAttachableHealthHarness(t)
	harness.step(t, io.EOF)
	requireHealthMonitorRunning(t, harness.done)
	harness.step(t, io.ErrUnexpectedEOF)
	waitAttachableHealthTest(t, harness.done, "fatal health failure")
}

func TestAttachableHealthFourSuccessesDoNotResetFailure(t *testing.T) {
	t.Parallel()
	harness := newAttachableHealthHarness(t)
	harness.step(t, errors.New("first failure"))
	for range 4 {
		harness.step(t, nil)
	}
	requireHealthMonitorRunning(t, harness.done)
	harness.step(t, errors.New("second failure"))
	waitAttachableHealthTest(t, harness.done, "fatal health failure")
}

func TestAttachableHealthFiveSuccessesResetFailure(t *testing.T) {
	t.Parallel()
	harness := newAttachableHealthHarness(t)
	harness.step(t, errors.New("first failure"))
	for range 5 {
		harness.step(t, nil)
	}
	harness.step(t, errors.New("failure after reset"))
	requireHealthMonitorRunning(t, harness.done)
	harness.step(t, errors.New("second failure after reset"))
	waitAttachableHealthTest(t, harness.done, "fatal health failure after reset")
}

func TestAttachableHealthBlackholeUsesBoundedTimeout(t *testing.T) {
	t.Parallel()
	ticks := make(chan time.Time)
	timeouts := make(chan time.Duration, 2)
	deadlineCancels := make(chan context.CancelCauseFunc, 2)
	checkEntered := make(chan struct{}, 2)
	checked := make(chan struct{}, 2)
	done := make(chan struct{})
	go func() {
		runAttachableHealthMonitor(t.Context(), attachableHealthMonitorConfig{
			ticks: ticks,
			now:   func() time.Time { return time.Unix(0, 0) },
			withTimeout: func(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
				timeouts <- timeout
				timeoutCtx, cancel := context.WithCancelCause(ctx)
				deadlineCancels <- cancel
				return timeoutCtx, func() { cancel(context.Canceled) }
			},
			check: func(ctx context.Context) error {
				checkEntered <- struct{}{}
				<-ctx.Done()
				return context.Cause(ctx)
			},
			testChecked: func() { checked <- struct{}{} },
		})
		close(done)
	}()

	for range 2 {
		ticks <- time.Time{}
		require.Equal(t, 30*time.Second, waitAttachableHealthTest(t, timeouts, "blackhole timeout"))
		cancelDeadline := waitAttachableHealthTest(t, deadlineCancels, "blackhole deadline")
		waitAttachableHealthTest(t, checkEntered, "blackholed health check")
		cancelDeadline(context.DeadlineExceeded)
		waitAttachableHealthTest(t, checked, "timed-out health check")
	}
	waitAttachableHealthTest(t, done, "fatal blackhole health failure")
}
