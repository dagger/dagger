package dagql

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/stretchr/testify/require"
)

// lockedBuffer is a buffer that concurrent log writers can share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// producerRemovedLogs captures, through the context's logger, the lines
// recording shared work that ended after its producer's session was removed.
type producerRemovedLogs struct {
	out lockedBuffer
}

func (l *producerRemovedLogs) into(ctx context.Context) context.Context {
	return slog.WithLogger(ctx, slog.New(slog.NewTextHandler(&l.out, nil)))
}

func (l *producerRemovedLogs) lines() []string {
	l.out.mu.Lock()
	defer l.out.mu.Unlock()
	var lines []string
	for _, line := range strings.Split(l.out.buf.String(), "\n") {
		if strings.Contains(line, "after its producer's session was removed") {
			lines = append(lines, line)
		}
	}
	return lines
}

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
		logs := &producerRemovedLogs{}
		base := cacheTestContext(t.Context())
		c, err := NewCache(base, "", nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.NoError(t, c.Close(ctx))
		})
		base = logs.into(ContextWithCache(base, c))
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
		lines := logs.lines()
		require.Len(t, lines, producers, "each removed producer's failure is logged once: %v", lines)
		for i, line := range lines {
			require.Contains(t, line, "failed after its producer's session was removed")
			require.Contains(t, line, "work=call")
			require.Contains(t, line, fmt.Sprintf("producerSession=producer-%d", i))
		}
	})
}

// A persisted result is decoded once for every session loading it: the first
// loader leads the decode under its own context and the others wait on it.
// When the leader's session is removed and its decode then fails, whatever the
// error, a waiter from a live session must lead a decode of its own instead of
// getting the leader's failure. In the engine a decode can need the leader's
// client, as a Module's does for its default dependencies, and that lookup
// reports a removed session as not found; the hook stands in for it.
func TestCachePersistenceImportedDecodeLeaderSessionRemovalRetriesJoiner(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	persistRetryDecodeSnapshotID.Store("")
	resultID := persistRetryDecodeSeed(t, ctx, dbPath, nil)
	logs := &producerRemovedLogs{}

	cB, err := NewCache(ctx, dbPath, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistRetryDecodeTestServer()

	const leaderSessionID = "persist-decode-removed-session-a"
	const joinerSessionID = "persist-decode-removed-session-b"
	loadCtx := func(sessionID string) (context.Context, *engine.ClientScopeAuthority) {
		loadCtx := ContextWithCache(logs.into(ctx), cB)
		loadCtx = srvToContext(loadCtx, srvB)
		return sharedCallTakeoverScope(t, loadCtx, sessionID)
	}

	var decodeEntries atomic.Int32
	var decodeSessions sync.Map
	firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
	// Deferred after the cache's close, so it runs first: a failed wait below
	// must not leave the leader's decode blocking the close.
	releaseLeader := sync.OnceFunc(func() { close(releaseFirst) })
	defer releaseLeader()
	persistRetryDecodeHooks.Store(resultID, func(hookCtx context.Context) error {
		md, err := engine.ClientMetadataFromContext(hookCtx)
		if err != nil {
			return err
		}
		decodeSessions.Store(md.SessionID, true)
		if decodeEntries.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
			return fmt.Errorf("session %q not found", md.SessionID)
		}
		return nil
	})
	defer persistRetryDecodeHooks.Delete(resultID)

	joined := make(chan struct{}, 4)
	cB.testPersistDecodeJoined = func(uint64) { joined <- struct{}{} }

	leaderCtx, leaderAuthority := loadCtx(leaderSessionID)
	leaderCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(leaderCtx, leaderSessionID, srvB, resultID)
		leaderCh <- err
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the leader to enter the persisted decode")
	}

	joinerCtx, _ := loadCtx(joinerSessionID)
	joinerCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(joinerCtx, joinerSessionID, srvB, resultID)
		joinerCh <- err
	}()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the joiner to park on the decode")
	}

	// The leader's session is removed, and its decode then fails.
	leaderAuthority.Close()
	releaseLeader()

	require.ErrorContains(t, <-leaderCh, "not found")
	require.NoError(t, <-joinerCh, "the joiner's session is alive; the decode it waited on must not fail it with another session's removal")
	require.Equal(t, int32(2), decodeEntries.Load())
	_, joinerDecoded := decodeSessions.Load(joinerSessionID)
	require.True(t, joinerDecoded, "the joiner led its own decode")
	lines := logs.lines()
	require.Len(t, lines, 1, "only the removed leader's decode is logged: %v", lines)
	require.Contains(t, lines[0], "failed after its producer's session was removed")
	require.Contains(t, lines[0], `work="persisted decode"`)
	require.Contains(t, lines[0], "producerSession="+leaderSessionID)

	require.NoError(t, cB.ReleaseSession(joinerCtx, joinerSessionID))
	require.NoError(t, cB.ReleaseSession(leaderCtx, leaderSessionID))
}

// Shared work whose producer's session is removed before the work's outcome
// is known keeps its value for its waiters when it succeeds, and the outcome
// is logged once, so how often waiting for such work pays off can be
// measured. The removal can come while the work runs, or after its callback
// returned but before its result is published.
func TestCacheSharedCallSucceedsAfterProducerRemoval(t *testing.T) {
	for _, removal := range []string{"while the work runs", "before publication"} {
		t.Run(removal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &producerRemovedLogs{}
				base := cacheTestContext(t.Context())
				c, err := NewCache(base, "", nil, nil)
				require.NoError(t, err)
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					require.NoError(t, c.Close(ctx))
				})
				base = logs.into(ContextWithCache(base, c))
				frame := cacheTestIntCall("shared-call-succeeds-after-removal")
				const concurrencyKey = "shared-call-succeeds-after-removal"

				producerScoped, producerAuthority := sharedCallTakeoverScope(t, base, "producer")
				producerCtx, cancelProducer := context.WithCancel(producerScoped)
				defer cancelProducer()
				waiterCtx, _ := sharedCallTakeoverScope(t, base, "waiter")
				started, release := make(chan struct{}), make(chan struct{})
				releaseWork := sync.OnceFunc(func() { close(release) })
				defer releaseWork()
				var runs atomic.Int32
				work := func(ctx context.Context) (AnyResult, error) {
					runs.Add(1)
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, context.Cause(ctx)
					}
					return cacheTestIntResult(frame, 1), nil
				}
				if removal == "before publication" {
					// The callback has returned; its waiters have not yet
					// published its result.
					c.testAfterCallbackWaiterCheck = producerAuthority.Close
				}

				producerDone := make(chan error, 1)
				go func() {
					_, err := c.GetOrInitCall(producerCtx, "producer", noopTypeResolver{}, &CallRequest{ResultCall: frame, ConcurrencyKey: concurrencyKey}, work)
					producerDone <- err
				}()
				<-started
				waiterDone := make(chan error, 1)
				go func() {
					_, err := c.GetOrInitCall(waiterCtx, "waiter", noopTypeResolver{}, &CallRequest{ResultCall: frame, ConcurrencyKey: concurrencyKey}, work)
					waiterDone <- err
				}()
				synctest.Wait()

				if removal == "while the work runs" {
					producerAuthority.Close()
				}
				cancelProducer()
				require.ErrorIs(t, <-producerDone, context.Canceled)
				releaseWork()

				require.NoError(t, <-waiterDone)
				require.Equal(t, int32(1), runs.Load(), "the waiter kept the work's value instead of running it again")
				lines := logs.lines()
				require.Len(t, lines, 1, "%v", lines)
				require.Contains(t, lines[0], "succeeded after its producer's session was removed")
				require.Contains(t, lines[0], "work=call")
				require.Contains(t, lines[0], "producerSession=producer")
			})
		})
	}
}
