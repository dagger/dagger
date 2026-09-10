package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestAgentClientScopeLifetime(t *testing.T) {
	var mu sync.Mutex
	var leases []*engine.ClientLifecycleLease
	var newLease func(engine.ClientLeaseKind, string) (*engine.ClientLifecycleLease, error)
	newLease = func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
		lease := engine.NewClientLifecycleLease(kind, owner, nil, newLease)
		mu.Lock()
		leases = append(leases, lease)
		mu.Unlock()
		return lease, nil
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
	request, err := newLease(engine.ClientLeaseRequest, "request")
	require.NoError(t, err)
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
