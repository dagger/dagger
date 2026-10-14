package dagql

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// sharedCallTakeoverScope returns ctx under a client scope of its own session,
// issued like a session's leases: under the session's authority, and refusing
// clones once the authority is closed, as a removed session does.
func sharedCallTakeoverScope(t *testing.T, ctx context.Context, session string) (context.Context, *engine.ClientScopeAuthority) {
	t.Helper()
	authority := engine.NewClientScopeAuthority()
	var lease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
	lease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
		return engine.NewClientLifecycleLeaseWithDelegation(kind, owner, func() {},
			func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
				if authority.Closed() {
					return nil, fmt.Errorf("session %q is closed", session)
				}
				return lease(kind, owner), nil
			}, nil, authority)
	}
	root := lease(engine.ClientLeaseRequest, "test request")
	t.Cleanup(root.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: session, ClientID: session + "-client"}, root)
	require.NoError(t, err)
	scoped, err := engine.ContextWithClientScope(ctx, scope)
	require.NoError(t, err)
	return scoped, authority
}

// A waiter takes a shared call over from a removed producer at most
// maxSharedCallTakeovers times. Each producer's session is removed while the
// waiter waits on its call, and the next producer has started the call again
// before the waiter wakes, so the waiter joins it instead of running the work
// itself. Once the budget is spent the waiter fails with the last producer's
// failure instead of waiting for more.
func TestCacheSharedCallTakeoverBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := cacheTestContext(t.Context())
		c, err := NewCache(base, "", nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.NoError(t, c.Close(ctx))
		})
		base = ContextWithCache(base, c)
		frame := cacheTestIntCall("shared-call-takeover-budget")
		const concurrencyKey = "shared-call-takeover-budget"

		const producers = maxSharedCallTakeovers + 1
		type producer struct {
			session   string
			ctx       context.Context
			cancel    context.CancelFunc
			authority *engine.ClientScopeAuthority
			started   chan struct{}
			release   chan struct{}
			done      chan error
		}
		ps := make([]*producer, producers)
		for i := range ps {
			p := &producer{
				session: fmt.Sprintf("producer-%d", i),
				started: make(chan struct{}),
				release: make(chan struct{}),
				done:    make(chan error, 1),
			}
			var scoped context.Context
			scoped, p.authority = sharedCallTakeoverScope(t, base, p.session)
			p.ctx, p.cancel = context.WithCancel(scoped)
			ps[i] = p
		}
		start := func(p *producer) {
			go func() {
				_, err := c.GetOrInitCall(p.ctx, p.session, noopTypeResolver{}, &CallRequest{ResultCall: frame, ConcurrencyKey: concurrencyKey}, func(context.Context) (AnyResult, error) {
					close(p.started)
					<-p.release
					return nil, errors.New(p.session + "'s dependency is gone")
				})
				p.done <- err
			}()
		}
		t.Cleanup(func() {
			for _, p := range ps {
				p.cancel()
				select {
				case <-p.release:
				default:
					close(p.release)
				}
			}
		})

		// Before a failed call wakes its waiters, the next producer starts the
		// call again.
		round := 0
		c.testAfterCallbackWaiterCheck = func() {
			round++
			if round < producers {
				start(ps[round])
				<-ps[round].started
			}
		}

		waiterCtx, _ := sharedCallTakeoverScope(t, base, "waiter")
		waiterRan := false
		waiterDone := make(chan error, 1)
		start(ps[0])
		<-ps[0].started
		go func() {
			_, err := c.GetOrInitCall(waiterCtx, "waiter", noopTypeResolver{}, &CallRequest{ResultCall: frame, ConcurrencyKey: concurrencyKey}, func(context.Context) (AnyResult, error) {
				waiterRan = true
				return cacheTestIntResult(frame, 1), nil
			})
			waiterDone <- err
		}()
		for _, p := range ps {
			// The waiter waits on p's call; then p's session is removed and the
			// work fails.
			synctest.Wait()
			c.callsMu.Lock()
			waiters := -1
			for keys, oc := range c.ongoingCalls {
				if keys.concurrencyKey == concurrencyKey {
					waiters = oc.waiters
				}
			}
			c.callsMu.Unlock()
			require.Equal(t, 2, waiters, "the waiter joined %s's call", p.session)
			p.authority.Close()
			p.cancel()
			require.ErrorIs(t, <-p.done, context.Canceled)
			close(p.release)
		}

		err = <-waiterDone
		require.ErrorContains(t, err, fmt.Sprintf("shared call takeover budget exhausted after %d foreign-session takeovers", maxSharedCallTakeovers))
		require.ErrorContains(t, err, ps[producers-1].session+"'s dependency is gone")
		require.False(t, waiterRan, "the waiter only ever joined another producer's call")
	})
}
