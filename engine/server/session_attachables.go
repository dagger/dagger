package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/v2/defaults"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
)

type sessionAttachableManager struct {
	mu           sync.Mutex
	callers      map[string]*sessionAttachableCaller
	reservations map[string]struct{}
	waiters      map[string][]chan struct{}

	testWaiterAdded func(string)
	testWaiterWoke  func(string)
}

type sessionAttachableCaller struct {
	ctx       context.Context
	conn      *grpc.ClientConn
	supported map[string]struct{}
	cancel    context.CancelCauseFunc
	done      chan struct{}
}

type sessionAttachableCallbacks struct {
	Published func()
	Removed   func(error)
}

var (
	errAttachableConnectionExists = errors.New("session attachables connection already exists")
	errAttachableExplicitClose    = errors.New("session attachment explicitly closed")
)

func newSessionAttachableManager() *sessionAttachableManager {
	return &sessionAttachableManager{
		callers:      map[string]*sessionAttachableCaller{},
		reservations: map[string]struct{}{},
		waiters:      map[string][]chan struct{}{},
	}
}

// Reserve atomically claims the pre-hijack registration slot for clientID. It
// is deliberately separate from Register so a duplicate can receive a normal
// machine-readable HTTP 409 before either connection is upgraded. Strict
// reservations also reject an inactive caller that has not finished removal.
func (m *sessionAttachableManager) Reserve(clientID string, strict bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.reservations[clientID]; ok {
		return errAttachableConnectionExists
	}
	if existing, ok := m.callers[clientID]; ok {
		if strict || existing.active() {
			return errAttachableConnectionExists
		}
	}
	m.reservations[clientID] = struct{}{}
	return nil
}

func (m *sessionAttachableManager) ReleaseReservation(clientID string) {
	m.mu.Lock()
	delete(m.reservations, clientID)
	m.wakeWaitersLocked(clientID)
	m.mu.Unlock()
}

func (m *sessionAttachableManager) Register(ctx context.Context, clientID string, conn net.Conn, methodURLs []string, callbacks sessionAttachableCallbacks) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)

	ctx, cc, err := attachableClientConn(ctx, conn)
	if err != nil {
		return err
	}

	caller := &sessionAttachableCaller{
		ctx:       ctx,
		conn:      cc,
		supported: map[string]struct{}{},
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	for _, methodURL := range methodURLs {
		caller.supported[strings.ToLower(methodURL)] = struct{}{}
	}

	m.mu.Lock()
	if _, reserved := m.reservations[clientID]; !reserved {
		m.mu.Unlock()
		return errAttachableExplicitClose
	}
	if existing, ok := m.callers[clientID]; ok && existing.active() {
		delete(m.reservations, clientID)
		m.mu.Unlock()
		return errAttachableConnectionExists
	}
	delete(m.reservations, clientID)
	m.callers[clientID] = caller
	m.wakeWaitersLocked(clientID)
	m.mu.Unlock()
	if callbacks.Published != nil {
		callbacks.Published()
	}

	defer func() {
		m.mu.Lock()
		owned := m.callers[clientID] == caller
		if owned {
			delete(m.callers, clientID)
			m.wakeWaitersLocked(clientID)
		}
		m.mu.Unlock()
		cause := context.Cause(caller.ctx)
		if owned && callbacks.Removed != nil {
			callbacks.Removed(cause)
		}
		close(caller.done)
	}()

	<-caller.ctx.Done()
	conn.Close()
	return nil
}

// Deregister cancels and waits for the exact published caller to leave the
// manager. Returning only after removal lets the explicit-close path satisfy
// the protocol ordering: deregister attachables first, then clear attachment
// ownership.
func (m *sessionAttachableManager) Deregister(clientID string) bool {
	m.mu.Lock()
	if _, ok := m.reservations[clientID]; ok {
		delete(m.reservations, clientID)
		m.wakeWaitersLocked(clientID)
		m.mu.Unlock()
		return true
	}
	caller, ok := m.callers[clientID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	caller.cancel(errAttachableExplicitClose)
	<-caller.done
	return true
}

func (m *sessionAttachableManager) Lookup(clientID string) (engineutil.SessionCaller, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	caller, ok := m.callers[clientID]
	if !ok || !caller.active() {
		return nil, false
	}
	return caller, true
}

func (m *sessionAttachableManager) Wait(
	ctx context.Context,
	clientID string,
	terminal func() error,
) (engineutil.SessionCaller, error) {
	for {
		m.mu.Lock()
		if caller, ok := m.callers[clientID]; ok && caller.active() {
			m.mu.Unlock()
			return caller, nil
		}
		if terminal != nil {
			if err := terminal(); err != nil {
				m.mu.Unlock()
				return nil, err
			}
		}

		waiter := make(chan struct{})
		m.waiters[clientID] = append(m.waiters[clientID], waiter)
		if m.testWaiterAdded != nil {
			m.testWaiterAdded(clientID)
		}
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.removeWaiterLocked(clientID, waiter)
			if caller, ok := m.callers[clientID]; ok && caller.active() {
				m.mu.Unlock()
				return caller, nil
			}
			if terminal != nil {
				if err := terminal(); err != nil {
					m.mu.Unlock()
					return nil, err
				}
			}
			m.mu.Unlock()
			return nil, fmt.Errorf("no active session attachables for client %q: %w", clientID, context.Cause(ctx))
		case <-waiter:
			if m.testWaiterWoke != nil {
				m.testWaiterWoke(clientID)
			}
		}
	}
}

func (m *sessionAttachableManager) removeWaiterLocked(clientID string, waiter chan struct{}) {
	waiters := m.waiters[clientID]
	for i, candidate := range waiters {
		if candidate == waiter {
			waiters = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(waiters) == 0 {
		delete(m.waiters, clientID)
		return
	}
	m.waiters[clientID] = waiters
}

func (m *sessionAttachableManager) wakeWaitersLocked(clientID string) {
	waiters := m.waiters[clientID]
	delete(m.waiters, clientID)
	for _, waiter := range waiters {
		close(waiter)
	}
}

func (caller *sessionAttachableCaller) active() bool {
	select {
	case <-caller.ctx.Done():
		return false
	default:
		return true
	}
}

func (caller *sessionAttachableCaller) Supports(method string) bool {
	_, ok := caller.supported[strings.ToLower(method)]
	return ok
}

func (caller *sessionAttachableCaller) Conn() *grpc.ClientConn {
	return caller.conn
}

func attachableClientConn(ctx context.Context, conn net.Conn) (context.Context, *grpc.ClientConn, error) {
	var dialCount int64
	dialer := grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		if c := atomic.AddInt64(&dialCount, 1); c > 1 {
			return nil, fmt.Errorf("only one connection allowed")
		}
		return conn, nil
	})

	dialOpts := []grpc.DialOption{
		dialer,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
	}

	cc, err := grpc.NewClient("passthrough:localhost", dialOpts...)
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to create grpc client: %w", err)
	}
	cc.Connect()

	ctx, cancel := context.WithCancelCause(ctx)
	go monitorAttachableHealth(ctx, cc, cancel)

	return ctx, cc, nil
}

func monitorAttachableHealth(ctx context.Context, cc *grpc.ClientConn, cancelConn func(error)) {
	defer cancelConn(context.Canceled)
	defer cc.Close()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	healthClient := grpc_health_v1.NewHealthClient(cc)

	failedBefore := false
	consecutiveSuccessful := 0
	defaultHealthcheckDuration := 30 * time.Second
	lastHealthcheckDuration := time.Duration(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			healthcheckStart := time.Now()
			timeout := time.Duration(math.Max(float64(defaultHealthcheckDuration), float64(lastHealthcheckDuration)*1.5))

			checkCtx, cancel := context.WithCancelCause(ctx)
			checkCtx, _ = context.WithTimeoutCause(checkCtx, timeout, context.DeadlineExceeded)
			_, err := healthClient.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{})
			cancel(context.Canceled)

			lastHealthcheckDuration = time.Since(healthcheckStart)

			if err != nil {
				select {
				case <-ctx.Done():
					slog.DebugContext(ctx, "context done, skipping healthcheck error")
					return
				default:
				}
				if failedBefore {
					slog.DebugContext(ctx, "healthcheck failed fatally")
					return
				}

				failedBefore = true
				consecutiveSuccessful = 0
				slog.DebugContext(ctx, "healthcheck failed",
					"timeout", timeout,
					"actualDuration", lastHealthcheckDuration,
				)
			} else {
				consecutiveSuccessful++

				if consecutiveSuccessful >= 5 && failedBefore {
					failedBefore = false
					slog.DebugContext(ctx, "reset healthcheck failure",
						"timeout", timeout,
						"actualDuration", lastHealthcheckDuration,
					)
				}
			}

			slog.TraceContext(ctx, "healthcheck completed",
				"timeout", timeout,
				"actualDuration", lastHealthcheckDuration,
			)
		}
	}
}
