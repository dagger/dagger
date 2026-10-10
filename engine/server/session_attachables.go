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
	mu      sync.Mutex
	callers map[string]*sessionAttachableCaller
	waiters map[string][]chan struct{}
	// expected holds the clients whose attachables a nested exec's session
	// helper will register. Waits for them last until the helper registers,
	// fails, or the exec ends, instead of a fixed timeout. A non-nil error
	// means it will not register (any more).
	expected map[string]*expectedAttachables
}

type expectedAttachables struct {
	err error
}

// errExecEnded fails waits for a nested exec's attachables once the exec is
// over.
var errExecEnded = errors.New("the exec ended before its Dagger session helper connected")

type sessionAttachableCaller struct {
	ctx       context.Context
	conn      *grpc.ClientConn
	supported map[string]struct{}
}

func newSessionAttachableManager() *sessionAttachableManager {
	return &sessionAttachableManager{
		callers:  map[string]*sessionAttachableCaller{},
		waiters:  map[string][]chan struct{}{},
		expected: map[string]*expectedAttachables{},
	}
}

func (m *sessionAttachableManager) Register(ctx context.Context, clientID string, conn net.Conn, methodURLs []string) error {
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
	}
	for _, methodURL := range methodURLs {
		caller.supported[strings.ToLower(methodURL)] = struct{}{}
	}

	m.mu.Lock()
	if existing, ok := m.callers[clientID]; ok && existing.active() {
		m.mu.Unlock()
		return fmt.Errorf("session attachables for client %q already exist", clientID)
	}
	m.callers[clientID] = caller
	m.wakeWaitersLocked(clientID)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		if m.callers[clientID] == caller {
			delete(m.callers, clientID)
			m.wakeWaitersLocked(clientID)
		}
		m.mu.Unlock()
	}()

	<-caller.ctx.Done()
	conn.Close()
	return nil
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

// Expect marks clientID's attachables as coming from a nested exec's session
// helper, which starts alongside the exec's command rather than before it.
// fail records that the helper will not register them; done records that the
// exec has ended. Either wakes current waits with the error, and makes later
// ones fail at once.
func (m *sessionAttachableManager) Expect(clientID string) (fail func(error), done func()) {
	m.mu.Lock()
	exp := &expectedAttachables{}
	m.expected[clientID] = exp
	m.mu.Unlock()
	fail = func(err error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if exp.err == nil {
			exp.err = err
			m.wakeWaitersLocked(clientID)
		}
	}
	return fail, func() { fail(errExecEnded) }
}

// waitContext bounds a wait for clientID's attachables by timeout (0 means
// none), unless they are expected from a nested exec's session helper: those
// waits last until the helper registers, fails, or the exec ends.
func (m *sessionAttachableManager) waitContext(ctx context.Context, clientID string, timeout time.Duration) (context.Context, context.CancelFunc) {
	m.mu.Lock()
	_, expected := m.expected[clientID]
	m.mu.Unlock()
	if expected || timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// WaitExpected waits for clientID's attachables if a nested exec's session
// helper is expected to register them, until it does, fails, or the exec
// ends. It returns at once for clients that aren't expected.
func (m *sessionAttachableManager) WaitExpected(ctx context.Context, clientID string) error {
	m.mu.Lock()
	_, expected := m.expected[clientID]
	m.mu.Unlock()
	if !expected {
		return nil
	}
	_, err := m.Wait(ctx, clientID)
	return err
}

func (m *sessionAttachableManager) Wait(ctx context.Context, clientID string) (engineutil.SessionCaller, error) {
	for {
		m.mu.Lock()
		if caller, ok := m.callers[clientID]; ok && caller.active() {
			m.mu.Unlock()
			return caller, nil
		}
		if exp := m.expected[clientID]; exp != nil && exp.err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("no session attachables for client %q: %w", clientID, exp.err)
		}

		waiter := make(chan struct{})
		m.waiters[clientID] = append(m.waiters[clientID], waiter)
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			m.removeWaiter(clientID, waiter)
			if caller, ok := m.Lookup(clientID); ok {
				return caller, nil
			}
			return nil, fmt.Errorf("no active session attachables for client %q: %w", clientID, context.Cause(ctx))
		case <-waiter:
		}
	}
}

func (m *sessionAttachableManager) removeWaiter(clientID string, waiter chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
