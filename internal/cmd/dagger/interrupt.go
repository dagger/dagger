package daggercmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/userconfig"
)

// defaultStopTimeout is how long the run's work gets to stop gracefully after
// the first interrupt, unless --stop-timeout or the stopTimeout user setting
// says otherwise.
const defaultStopTimeout = 10 * time.Second

// interruptRequestTimeout bounds each request that tells the engine about an
// interrupt, so an unresponsive engine can't hold up Ctrl-C.
const interruptRequestTimeout = 2 * time.Second

// stopTimeoutFlag is the --stop-timeout flag. It records whether it was set,
// so the stopTimeout user setting applies only when it was not.
type stopTimeoutFlag struct {
	value time.Duration
	set   bool
}

var stopTimeout stopTimeoutFlag

func (f *stopTimeoutFlag) String() string {
	if !f.set {
		return defaultStopTimeout.String()
	}
	return f.value.String()
}

func (f *stopTimeoutFlag) Set(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if d < 0 {
		return fmt.Errorf("must not be negative")
	}
	f.value, f.set = d, true
	return nil
}

func (f *stopTimeoutFlag) Type() string {
	return "duration"
}

// effective returns the stop timeout: the flag, else the user setting, else
// the default.
func (f *stopTimeoutFlag) effective() time.Duration {
	if f.set {
		return f.value
	}
	if d, ok := userconfig.StopTimeout(); ok {
		return d
	}
	return defaultStopTimeout
}

// interrupts turns the CLI's interrupts into the engine's two-step stop. On
// the first interrupt (Ctrl-C, SIGINT or SIGTERM) the engine gives the run's
// services and the execs its cancelled requests leave SIGTERM, and SIGKILL
// after the stop timeout. On the second, it kills them at once.
var interrupts = &cliInterrupts{sessions: map[*client.Client]struct{}{}}

type cliInterrupts struct {
	mu       sync.Mutex
	sessions map[*client.Client]struct{}
	count    int
}

// attach makes interrupts reach sess's engine until the returned func is
// called.
func (i *cliInterrupts) attach(sess *client.Client) func() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.sessions[sess] = struct{}{}
	return func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		delete(i.sessions, sess)
	}
}

// interrupt tells the engine of every attached session about an interrupt:
// the first asks for a graceful stop, and any later one forces it. It returns
// once the engine has been told, so the caller can then cancel the run's
// requests. It reports whether this interrupt forced the stop.
func (i *cliInterrupts) interrupt() (force bool) {
	i.mu.Lock()
	i.count++
	force = i.count > 1
	sessions := make([]*client.Client, 0, len(i.sessions))
	for sess := range i.sessions {
		sessions = append(sessions, sess)
	}
	i.mu.Unlock()

	timeout := stopTimeout.effective()
	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), interruptRequestTimeout)
			defer cancel()
			if err := sess.Interrupt(ctx, timeout, force); err != nil {
				slog.Warn("failed to interrupt session", "error", err)
			}
		})
	}
	wg.Wait()
	return force
}

// interruptSignals handles SIGINT and SIGTERM for the whole command. The
// first tells the engine to stop the run's work gracefully, then cancels the
// returned context. A later one forces the stop and exits at once. The TUI
// reads Ctrl-C as a key press instead, and does the same (see
// dagui.FrontendOpts.OnInterrupt).
func interruptSignals(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-sigCh:
				if interrupts.interrupt() {
					slog.Warn("exiting immediately")
					os.Exit(2)
				}
				slog.Warn("canceling... (press again to exit immediately)", "signal", sig)
				cancel(context.Canceled)
			}
		}
	}()
	return ctx, func() {
		signal.Stop(sigCh)
		close(done)
		cancel(context.Canceled)
	}
}
