package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestAgentClientScopeLifetime(t *testing.T) {
	var mu sync.Mutex
	var leases []*engine.ClientLifecycleLease
	var newLease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
	newLease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
		lease := engine.NewClientLifecycleLease(kind, owner, nil, func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
			return newLease(kind, owner), nil
		})
		mu.Lock()
		leases = append(leases, lease)
		mu.Unlock()
		return lease
	}
	held := func(kind engine.ClientLeaseKind) int {
		mu.Lock()
		defer mu.Unlock()
		count := 0
		for _, lease := range leases {
			if lease.Kind() == kind && lease.Held() {
				count++
			}
		}
		return count
	}
	request := newLease(engine.ClientLeaseRequest, "request")
	scope, err := engine.NewClientScope(&engine.ClientMetadata{ClientID: "client", SessionID: "session"}, request)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	ctx = testAgentContext(t, ctx, "leased-agent", "agent")
	agent, ok := AgentFromContext(ctx)
	require.True(t, ok)
	registry := NewAgentRuntimes()
	t.Cleanup(func() { require.NoError(t, registry.KillAll(context.Background(), nil)) })
	rt, err := registry.Create(ctx, agent, AgentStateIdle, "", false)
	require.NoError(t, err)
	require.Equal(t, 1, held(engine.ClientLeaseAgentTombstone))
	// Park before starting so this unit test needs no model or engine query.
	rt.paused = true
	require.NoError(t, rt.start(ctx))
	require.NoError(t, rt.start(ctx), "duplicate starts must not acquire another lease")
	require.Equal(t, 1, held(engine.ClientLeaseAgent))
	request.Release()
	require.Equal(t, 1, held(engine.ClientLeaseAgent), "request completion must not release the loop")
	require.NoError(t, rt.Stop(t.Context(), true, nil, AgentStopExplicit))
	require.Eventually(t, func() bool { return held(engine.ClientLeaseAgent) == 0 }, time.Second, time.Millisecond)
	require.Equal(t, 1, held(engine.ClientLeaseAgentTombstone), "the stopped snapshot remains addressable")
	require.NoError(t, registry.KillAll(t.Context(), nil))
	require.Zero(t, held(engine.ClientLeaseAgentTombstone))
}

func TestAgentCaptureRetainsExecutableScope(t *testing.T) {
	var acquired, released atomic.Int32
	var newLease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
	newLease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
		acquired.Add(1)
		return engine.NewClientLifecycleLease(kind, owner, func() { released.Add(1) }, func(k engine.ClientLeaseKind, o string) (*engine.ClientLifecycleLease, error) {
			return newLease(k, o), nil
		})
	}
	request := newLease(engine.ClientLeaseRequest, "request")
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: "session", ClientID: "client"}, request)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	rt := &AgentRuntime{key: "capture", spanCtx: agentTelemetryContext(ctx)}
	rt.captureConversationLocked(ctx)
	capture := rt.controlCapture
	request.Release() // resolver has returned before capture starts
	_, child, err := engine.DetachClientScope(capture.ctx, engine.ClientLeaseSharedWork, "recipe-selection")
	require.NoError(t, err, "queued recipe evaluation must use its own held scope, not the expired request holder")
	child.Release()
	_, err = capture.resolve()
	require.ErrorContains(t, err, "no committed conversation")
	require.Equal(t, acquired.Load(), released.Load(), "capture failure releases its scope")
}

func TestAgentCaptureCoalescingReleasesScopes(t *testing.T) {
	var released atomic.Int32
	capture := func() *agentCapture {
		c := &agentCapture{lease: engine.NewClientLifecycleLease(engine.ClientLeaseSharedWork, "capture", func() { released.Add(1) }, nil)}
		c.refs.Store(1)
		return c
	}
	p := &agentControlPublisher{wake: make(chan struct{}, 1)}
	old, next := capture(), capture()
	p.enqueue(agentControlJob{capture: old})
	old.release() // replaced runtime tip, publisher still owns pending capture
	require.Zero(t, released.Load())
	p.enqueue(agentControlJob{capture: next})
	require.EqualValues(t, 1, released.Load(), "superseded pending capture releases its executable scope")
	next.release()
	p.pending.capture.release()
	require.EqualValues(t, 2, released.Load())
}

func TestAgentCreateLeaseOutsideRegistryLock(t *testing.T) {
	registry := NewAgentRuntimes()
	var acquired, released atomic.Int32
	entered := make(chan struct{}, 2)
	proceed := make(chan struct{})
	request := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "request", nil,
		func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
			if kind != engine.ClientLeaseAgentTombstone {
				return engine.NewClientLifecycleLease(kind, owner, nil, nil), nil
			}
			// Lifecycle callbacks must be able to inspect the registry, including
			// while two constructors for the same handle are staging.
			registry.mu.Lock()
			registry.mu.Unlock()
			entered <- struct{}{}
			<-proceed
			acquired.Add(1)
			return engine.NewClientLifecycleLease(kind, owner, func() { released.Add(1) }, nil), nil
		})
	scope, err := engine.NewClientScope(&engine.ClientMetadata{ClientID: "client", SessionID: "session"}, request)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	ctx = testAgentContext(t, ctx, "raced-agent", "agent")
	agent, ok := AgentFromContext(ctx)
	require.True(t, ok)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := registry.Create(ctx, agent, AgentStateIdle, "", false)
			results <- err
		}()
	}
	// Ensure both constructors acquired outside the lock before publishing.
	for range 2 {
		select {
		case <-entered:
		case err := <-results:
			t.Fatalf("creation returned before lease barrier: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("lease acquisition blocked")
		}
	}
	close(proceed)
	first, second := <-results, <-results
	require.NotEqual(t, first == nil, second == nil, "only one constructor may publish")
	require.EqualValues(t, 2, acquired.Load())
	require.EqualValues(t, 1, released.Load(), "losing constructor releases its lease")
	require.NoError(t, registry.KillAll(t.Context(), nil))
	require.EqualValues(t, 2, released.Load(), "teardown releases the winning lease")
}
