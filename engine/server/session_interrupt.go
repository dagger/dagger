package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

// sessionInterrupt is a session's graceful interrupt, which its main client
// asks for on its first interrupt (Ctrl-C, SIGTERM). From then on, the
// session's services and the execs its cancelled requests leave get SIGTERM
// instead of SIGKILL, and SIGKILL once its deadline passes or the interrupt
// is forced (a second interrupt, or the session going away).
type sessionInterrupt struct {
	mu       sync.Mutex
	deadline time.Time
	force    chan struct{}
	forced   bool

	// pending counts the stops still running gracefully; idle is closed when
	// it drops to zero.
	pending int
	idle    chan struct{}
}

func newSessionInterrupt() *sessionInterrupt {
	return &sessionInterrupt{force: make(chan struct{})}
}

// begin starts a graceful interrupt that lasts stopTimeout, and reports
// whether this call started it. A non-positive stopTimeout forces it instead.
func (i *sessionInterrupt) begin(stopTimeout time.Duration) (deadline time.Time, force <-chan struct{}, started bool) {
	if stopTimeout <= 0 {
		i.forceStop()
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.forced || !i.deadline.IsZero() {
		return i.deadline, i.force, false
	}
	i.deadline = time.Now().Add(stopTimeout)
	return i.deadline, i.force, true
}

// forceStop ends the graceful interrupt: whatever is still stopping gets
// SIGKILL now, and anything stopped later is killed at once.
func (i *sessionInterrupt) forceStop() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.forced {
		i.forced = true
		close(i.force)
	}
}

// gracefulStop implements engineutil.Opts.GracefulStop for the session's
// execs: a graceful stop until the deadline while the session is interrupted
// and not forced, and SIGKILL otherwise.
func (i *sessionInterrupt) gracefulStop() (time.Time, <-chan struct{}, func()) {
	if i == nil {
		return time.Time{}, nil, func() {}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.forced || i.deadline.IsZero() {
		return time.Time{}, nil, func() {}
	}
	return i.deadline, i.force, i.trackLocked()
}

// track counts a graceful stop until the returned func is called.
func (i *sessionInterrupt) track() func() {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.trackLocked()
}

func (i *sessionInterrupt) trackLocked() func() {
	i.pending++
	if i.pending == 1 {
		i.idle = make(chan struct{})
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			i.mu.Lock()
			defer i.mu.Unlock()
			i.pending--
			if i.pending == 0 {
				close(i.idle)
			}
		})
	}
}

// wait returns once no graceful stop is running, or ctx is done.
func (i *sessionInterrupt) wait(ctx context.Context) error {
	if i == nil {
		return nil
	}
	for {
		i.mu.Lock()
		if i.pending == 0 {
			i.mu.Unlock()
			return nil
		}
		idle := i.idle
		i.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// interrupted reports whether a graceful interrupt was asked for.
func (i *sessionInterrupt) interrupted() bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return !i.deadline.IsZero() || i.forced
}

// serveInterrupt handles the main client's interrupts: the first starts a
// graceful interrupt of the session, which stops its services right away;
// a forced one kills what is still stopping.
func (srv *Server) serveInterrupt(w http.ResponseWriter, r *http.Request, client *clientRuntime) error {
	sess := client.daggerSession
	if sess.interrupt == nil {
		return httpErr(errors.New("session cannot be interrupted"), http.StatusConflict)
	}
	if client.clientID != sess.mainClientCallerID {
		return httpErr(errors.New("only the session's main client can interrupt it"), http.StatusForbidden)
	}
	query := r.URL.Query()
	slog := slog.With("sessionID", sess.sessionID)

	if force, _ := strconv.ParseBool(query.Get(engine.InterruptForceParam)); force {
		slog.Info("session interrupt forced")
		sess.interrupt.forceStop()
		w.WriteHeader(http.StatusNoContent)
		return nil
	}

	stopTimeout, err := time.ParseDuration(query.Get(engine.InterruptStopTimeoutParam))
	if err != nil {
		return httpErr(fmt.Errorf("invalid %s: %w", engine.InterruptStopTimeoutParam, err), http.StatusBadRequest)
	}
	deadline, force, started := sess.interrupt.begin(stopTimeout)
	if started {
		slog.Info("session interrupted; stopping its work gracefully", "stopTimeout", stopTimeout)
		done := sess.interrupt.track()
		ctx := context.WithoutCancel(r.Context())
		go func() {
			defer done()
			if err := sess.services.StopSessionServicesGracefully(ctx, sess.sessionID, deadline, force); err != nil {
				slog.Warn("error stopping services gracefully", "error", err)
			}
		}()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
