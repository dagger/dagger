package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeSessionCaller struct {
	id   string
	conn *grpc.ClientConn
}

func TestCloseKeepAliveTelemetryDBTransfersOwnership(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)

	client := &daggerClient{keepAliveTelemetryDB: db}
	require.NoError(t, client.closeKeepAliveTelemetryDB())
	require.Nil(t, client.keepAliveTelemetryDB)
	// A cleanup and teardown path converging on the same client is a no-op
	// after the first path transfers the pointer.
	require.NoError(t, client.closeKeepAliveTelemetryDB())
}

func (caller *fakeSessionCaller) Supports(string) bool {
	return false
}

func (caller *fakeSessionCaller) Conn() *grpc.ClientConn {
	return caller.conn
}

func TestActiveClientIDsConcurrentSessionClientMutation(t *testing.T) {
	t.Parallel()

	// Regression test: activeClientIDs must read sess.clients under clientMu.
	// Without the lock, ranging the map while another goroutine writes it is a
	// fatal "concurrent map iteration and map write" (caught here under -race).
	sess := &daggerSession{
		clients: map[string]*daggerClient{
			"client-a": {clientID: "client-a"},
		},
	}
	sess.state.Store(sessionStateInitialized)
	srv := &Server{
		daggerSessions: map[string]*daggerSession{
			"session-a": sess,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sess.clientMu.Lock()
			sess.clients["transient"] = &daggerClient{clientID: "transient"}
			delete(sess.clients, "transient")
			sess.clientMu.Unlock()
		}
	}()
	<-started

	for i := 0; i < 1000; i++ {
		require.True(t, srv.activeClientIDs()["client-a"])
	}
}

func TestClientFromIDsConcurrentSessionInitialization(t *testing.T) {
	t.Parallel()

	// Regression test: clientFromIDs must read sess.state (atomically) and
	// sess.clients (under clientMu) while another goroutine mutates them during
	// session initialization. Without that discipline this is a data race (caught
	// here under -race).
	sess := &daggerSession{}
	sess.state.Store(sessionStateUninitialized)
	srv := &Server{
		daggerSessions: map[string]*daggerSession{
			"session-a": sess,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_, _ = srv.clientFromIDs("session-a", "client-a")
		}
	}()
	<-started

	for i := 0; i < 1000; i++ {
		sess.clientMu.Lock()
		sess.clients = map[string]*daggerClient{
			"client-a": {clientID: "client-a"},
		}
		sess.clientMu.Unlock()
		sess.state.Store(sessionStateInitialized)
		sess.state.Store(sessionStateUninitialized)
		sess.clientMu.Lock()
		sess.clients = nil
		sess.clientMu.Unlock()
	}

	client := &daggerClient{clientID: "client-a"}
	sess.clientMu.Lock()
	sess.clients = map[string]*daggerClient{
		client.clientID: client,
	}
	sess.clientMu.Unlock()
	sess.state.Store(sessionStateInitialized)

	got, err := srv.clientFromIDs("session-a", client.clientID)
	require.NoError(t, err)
	require.Same(t, client, got)
}

func TestClientsDoesNotBlockWhileSessionLifecycleLocked(t *testing.T) {
	t.Parallel()

	// Regression for the >15s active-clients stall (Discord: "Session lock might
	// be causing unwanted session shutdowns"): Clients() must never acquire a
	// session's lifecycleMu. A session stuck initializing or tearing down holds
	// lifecycleMu for a long time (teardown has a 60s safeguard), but that must
	// not stall the active-clients API the cloud keepalive polls.
	live := &daggerSession{sessionID: "live", mainClientCallerID: "main-live"}
	live.state.Store(sessionStateInitialized)
	busy := &daggerSession{sessionID: "busy", mainClientCallerID: "main-busy"}
	busy.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{
		"live": live,
		"busy": busy,
	}}

	// Simulate an in-progress init/teardown holding busy's lifecycleMu.
	busy.lifecycleMu.Lock()
	defer busy.lifecycleMu.Unlock()

	done := make(chan []string, 1)
	go func() { done <- srv.Clients() }()

	select {
	case clients := <-done:
		require.ElementsMatch(t, []string{"main-live", "main-busy"}, clients)
	case <-time.After(10 * time.Second):
		t.Fatal("Clients() blocked while a session's lifecycleMu was held")
	}
}

func TestActiveClientIDsDoesNotBlockWhileSessionLifecycleLocked(t *testing.T) {
	t.Parallel()

	// activeClientIDs() (the client-DB GC ticker) must also never acquire a
	// session's lifecycleMu, for the same reason as Clients().
	live := &daggerSession{
		sessionID: "live",
		clients:   map[string]*daggerClient{"c-live": {clientID: "c-live"}},
	}
	live.state.Store(sessionStateInitialized)
	busy := &daggerSession{
		sessionID: "busy",
		clients:   map[string]*daggerClient{"c-busy": {clientID: "c-busy"}},
	}
	busy.state.Store(sessionStateInitialized)
	srv := &Server{daggerSessions: map[string]*daggerSession{
		"live": live,
		"busy": busy,
	}}

	busy.lifecycleMu.Lock()
	defer busy.lifecycleMu.Unlock()

	done := make(chan map[string]bool, 1)
	go func() { done <- srv.activeClientIDs() }()

	select {
	case keep := <-done:
		require.True(t, keep["c-live"], "expected live session's client to be kept")
		require.True(t, keep["c-busy"], "expected initialized busy session's client to be kept")
	case <-time.After(10 * time.Second):
		t.Fatal("activeClientIDs() blocked while a session's lifecycleMu was held")
	}
}

func TestGetOrInitClientReturnsFastForRemovedTombstone(t *testing.T) {
	t.Parallel()

	// A session mid-teardown holds lifecycleMu and is marked removed (a tombstone
	// left in the registry until cleanup completes). A same-id getOrInitClient
	// must bail immediately via the lock-free removed pre-check rather than block
	// on lifecycleMu for the (possibly ~60s) teardown.
	tombstone := &daggerSession{sessionID: "s", mainClientCallerID: "m"}
	tombstone.state.Store(sessionStateRemoved)
	srv := &Server{daggerSessions: map[string]*daggerSession{"s": tombstone}}

	// Hold lifecycleMu to simulate an in-progress teardown.
	tombstone.lifecycleMu.Lock()
	defer tombstone.lifecycleMu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, _, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
			ClientMetadata: &engine.ClientMetadata{
				SessionID:         "s",
				ClientID:          "m",
				ClientSecretToken: "token",
			},
		})
		done <- err
	}()

	select {
	case err := <-done:
		var retryable flightcontrol.RetryableError
		require.ErrorAs(t, err, &retryable, "removed tombstone should yield a retryable error")
	case <-time.After(10 * time.Second):
		t.Fatal("getOrInitClient blocked on lifecycleMu for a removed tombstone")
	}
}

func TestClientFromIDsStateGating(t *testing.T) {
	t.Parallel()

	// clientFromIDs gates on the session's (atomic) lifecycle state without ever
	// taking lifecycleMu, and never returns a client whose session isn't usable.
	client := &daggerClient{clientID: "c"}
	sess := &daggerSession{
		sessionID: "s",
		clients:   map[string]*daggerClient{"c": client},
	}
	srv := &Server{daggerSessions: map[string]*daggerSession{"s": sess}}

	// uninitialized: not yet usable.
	sess.state.Store(sessionStateUninitialized)
	_, err := srv.clientFromIDs("s", "c")
	require.ErrorContains(t, err, "not initialized")

	// removed: retryable not-found (session is tearing down).
	sess.state.Store(sessionStateRemoved)
	_, err = srv.clientFromIDs("s", "c")
	var retryable flightcontrol.RetryableError
	require.ErrorAs(t, err, &retryable)

	// initialized: returns the client.
	sess.state.Store(sessionStateInitialized)
	got, err := srv.clientFromIDs("s", "c")
	require.NoError(t, err)
	require.Same(t, client, got)
}

func TestSessionLifecycleObserverConcurrency(t *testing.T) {
	t.Parallel()

	// Stress the observer paths (Clients/activeClientIDs/clientFromIDs) against
	// concurrent session churn. The churners exercise the observer-visible state
	// the way the real lifecycle does — registry writes under daggerSessionsMu,
	// the clients map under clientMu, the lifecycle state via the atomic, and a
	// pointer-conditional deleteSession on teardown — but deliberately do NOT take
	// lifecycleMu, since the whole point of the redesign is that observers don't
	// depend on it. Run under -race to catch data races; the observers must also
	// never block (completing while churn runs is the liveness assertion).
	srv := &Server{daggerSessions: map[string]*daggerSession{}}

	const (
		churners         = 4
		cyclesPerChurner = 1000
	)
	var wg sync.WaitGroup
	for i := range churners {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("s%d", n)
			for range cyclesPerChurner {
				sess := &daggerSession{
					sessionID:          id,
					mainClientCallerID: "m" + id,
					clients:            map[string]*daggerClient{},
				}
				// publish, then populate clients, then flip to initialized last.
				srv.daggerSessionsMu.Lock()
				srv.daggerSessions[id] = sess
				srv.daggerSessionsMu.Unlock()
				sess.clientMu.Lock()
				sess.clients["c"] = &daggerClient{clientID: "c"}
				sess.clientMu.Unlock()
				sess.state.Store(sessionStateInitialized)

				// teardown: removed first, then pointer-conditional delete.
				sess.state.Store(sessionStateRemoved)
				srv.deleteSession(sess)
			}
		}(i)
	}

	churnDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(churnDone)
	}()

	// Hammer the observers concurrently until every churner has finished its
	// fixed workload, so the race window is exercised deterministically rather
	// than depending on scheduler timing.
	for {
		select {
		case <-churnDone:
			return
		default:
		}
		_ = srv.Clients()
		_ = srv.activeClientIDs()
		_, _ = srv.clientFromIDs("s0", "c")
	}
}

// newTeardownTestServer builds a Server with just enough real state for
// removeDaggerSession to run: an empty in-memory dagql cache, a live wcprof
// counter, and a stubbed GC callback (scheduled via time.AfterFunc at the end
// of teardown).
func newTeardownTestServer(t *testing.T) *Server {
	t.Helper()
	cache, err := dagql.NewCache(context.Background(), "", nil, nil)
	require.NoError(t, err)
	return &Server{
		daggerSessions:  map[string]*daggerSession{},
		engineCache:     cache,
		wcprofSpanCount: newWcprofSpanCounter(),
		throttledGC:     func() {},
	}
}

// newTeardownTestSession publishes an initialized session whose main client
// has the given number of active connections. dagqlInFlight starts at 1 so
// teardown deterministically blocks in the in-flight drain until
// releaseTeardownDrain is called.
func newTeardownTestSession(srv *Server, sessionID, mainClientID string, activeCount int) (*daggerSession, *daggerClient) {
	client := &daggerClient{
		clientID:    mainClientID,
		activeCount: activeCount,
	}
	sess := &daggerSession{
		sessionID:          sessionID,
		mainClientCallerID: mainClientID,
		clients:            map[string]*daggerClient{mainClientID: client},
		services:           core.NewServices(),
		analytics:          analytics.New(analytics.Config{DoNotTrack: true}),
		shutdownCh:         make(chan struct{}),
	}
	client.daggerSession = sess
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.dagqlInFlight = 1
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.state.Store(sessionStateInitialized)

	srv.daggerSessionsMu.Lock()
	srv.daggerSessions[sessionID] = sess
	srv.daggerSessionsMu.Unlock()
	return sess, client
}

func releaseTeardownDrain(sess *daggerSession) {
	sess.dagqlMu.Lock()
	sess.dagqlInFlight = 0
	sess.dagqlCond.Broadcast()
	sess.dagqlMu.Unlock()
}

func sessionInRegistry(srv *Server, sessionID string) bool {
	srv.daggerSessionsMu.RLock()
	defer srv.daggerSessionsMu.RUnlock()
	_, ok := srv.daggerSessions[sessionID]
	return ok
}

func TestMainClientLastDisconnectDoesNotBlockOnTeardown(t *testing.T) {
	t.Parallel()

	// Regression for the client-side `shutdown: ... context deadline exceeded`
	// timeout: the main client's last connection cleanup runs in the request
	// handler before the /shutdown response is flushed, so it must only
	// SCHEDULE teardown, never run it. Teardown here is deterministically
	// blocked in the in-flight dagql drain; the cleanup must return anyway.
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 1)

	done := make(chan error, 1)
	go func() {
		done <- srv.releaseClientConnection(context.Background(), sess, client)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("last-connection cleanup blocked on session teardown")
	}

	// The background reap marks the session removed (tombstone) but stays
	// blocked in the drain, so the registry entry must survive for now.
	require.Eventually(t, func() bool {
		return sess.state.Load() == sessionStateRemoved
	}, 10*time.Second, 10*time.Millisecond, "background teardown never started")
	require.True(t, sessionInRegistry(srv, "s"), "tombstone dropped before teardown finished")

	releaseTeardownDrain(sess)

	require.Eventually(t, func() bool {
		return !sessionInRegistry(srv, "s")
	}, 10*time.Second, 10*time.Millisecond, "session never finished background teardown")
	select {
	case <-sess.shutdownCh:
	case <-time.After(10 * time.Second):
		t.Fatal("session shutdownCh never closed by background teardown")
	}
}

func TestSameIDConnectDuringBackgroundTeardownGetsRetryable(t *testing.T) {
	t.Parallel()

	// While a background reap is mid-teardown (removed tombstone in the
	// registry, lifecycleMu held), a same-id getOrInitClient must bail out
	// fast with a retryable error instead of blocking on the teardown.
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 1)

	require.NoError(t, srv.releaseClientConnection(context.Background(), sess, client))
	require.Eventually(t, func() bool {
		return sess.state.Load() == sessionStateRemoved
	}, 10*time.Second, 10*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, _, err := srv.getOrInitClient(context.Background(), &ClientInitOpts{
			ClientMetadata: &engine.ClientMetadata{
				SessionID:         "s",
				ClientID:          "m",
				ClientSecretToken: "token",
			},
		})
		done <- err
	}()
	select {
	case err := <-done:
		var retryable flightcontrol.RetryableError
		require.ErrorAs(t, err, &retryable)
	case <-time.After(10 * time.Second):
		t.Fatal("same-id getOrInitClient blocked on background teardown")
	}

	releaseTeardownDrain(sess)
	require.Eventually(t, func() bool {
		return !sessionInRegistry(srv, "s")
	}, 10*time.Second, 10*time.Millisecond)
}

func TestReapAbandonedWhenMainClientReconnects(t *testing.T) {
	t.Parallel()

	// A new main-client connection can land between the last disconnect and
	// the scheduled reap running. The reap re-checks activeCount under
	// lifecycleMu and must leave the now-live session alone.
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 1)

	// Simulate the reconnect winning the race: activeCount is back above zero
	// by the time the reap runs.
	client.stateMu.Lock()
	client.activeCount = 1
	client.stateMu.Unlock()

	srv.reapDaggerSession(context.Background(), sess, client)

	require.Equal(t, sessionStateInitialized, sess.state.Load())
	require.True(t, sessionInRegistry(srv, "s"))
	select {
	case <-sess.shutdownCh:
		t.Fatal("reap tore down a session with a live main client")
	default:
	}
}

func TestConcurrentReapsSingleTeardown(t *testing.T) {
	t.Parallel()

	// Duplicate reaps can be scheduled (disconnect, reconnect, disconnect).
	// Whichever acquires lifecycleMu first tears down; the loser must observe
	// the removed state and no-op (double teardown would double-close
	// shutdownCh and panic).
	srv := newTeardownTestServer(t)
	sess, client := newTeardownTestSession(srv, "s", "m", 0)

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.reapDaggerSession(context.Background(), sess, client)
		}()
	}

	require.Eventually(t, func() bool {
		return sess.state.Load() == sessionStateRemoved
	}, 10*time.Second, 10*time.Millisecond)
	releaseTeardownDrain(sess)
	wg.Wait()

	require.False(t, sessionInRegistry(srv, "s"))
	select {
	case <-sess.shutdownCh:
	default:
		t.Fatal("session shutdownCh not closed after teardown")
	}
}

func TestPendingLegacyModule(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Root: "/repo", Cwd: "."}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return "/resolved/" + relPath
	}

	t.Run("preserves remote pin", func(t *testing.T) {
		t.Parallel()

		mod := pendingLegacyModule(
			ws,
			resolveLocalRef,
			"go",
			"github.com/acme/go-toolchain@main",
			"abc123",
			false,
			map[string]any{"foo": "bar"},
			[]*modules.ModuleConfigArgument{{
				Argument:    "config",
				DefaultPath: "./custom-config.txt",
			}},
		)

		require.Equal(t, "github.com/acme/go-toolchain@main", mod.Ref)
		require.Equal(t, "abc123", mod.RefPin)
		require.Equal(t, "go", mod.Name)
		require.False(t, mod.Entrypoint)
		require.True(t, mod.LegacyDefaultPath)
		require.Equal(t, "/resolved/.", mod.DefaultPathContextSourceRef)
		require.Equal(t, map[string]any{"foo": "bar"}, mod.ConfigDefaults)
		require.Len(t, mod.ArgCustomizations, 1)
		require.Equal(t, "./custom-config.txt", mod.ArgCustomizations[0].DefaultPath)
	})

	t.Run("resolves local refs without ref pin", func(t *testing.T) {
		t.Parallel()

		mod := pendingLegacyModule(
			ws,
			resolveLocalRef,
			"blueprint",
			"../blueprint",
			"",
			true,
			nil,
			nil,
		)

		require.Equal(t, "/resolved/../blueprint", mod.Ref)
		require.Empty(t, mod.RefPin)
		require.Equal(t, "blueprint", mod.Name)
		require.True(t, mod.Entrypoint)
		require.True(t, mod.LegacyDefaultPath)
		require.Equal(t, "/resolved/.", mod.DefaultPathContextSourceRef)
		require.Nil(t, mod.ConfigDefaults)
	})
}

func TestFilterPendingWorkspaceModulesForRootFields(t *testing.T) {
	t.Parallel()

	mods := []pendingModule{
		{Kind: moduleLoadKindAmbient, Name: "foo", Entrypoint: false},
		{Kind: moduleLoadKindAmbient, Name: "bar-baz", Entrypoint: true},
		{Kind: moduleLoadKindAmbient, Name: "local", Entrypoint: true},
	}

	t.Run("constructor match loads only matching module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"foo"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("unknown root field with multiple entrypoints loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"doThing"})
		require.Equal(t, mods, filtered)
	})

	t.Run("unknown root field with one entrypoint loads entrypoint", func(t *testing.T) {
		t.Parallel()

		oneEntrypoint := []pendingModule{mods[0], mods[1]}
		filtered := filterPendingWorkspaceModulesForRootFields(oneEntrypoint, nil, []string{"doThing"})
		require.Equal(t, []pendingModule{mods[1]}, filtered)
	})

	t.Run("introspection loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"__schema"})
		require.Equal(t, mods, filtered)
	})

	t.Run("current typedefs loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"currentTypeDefs"})
		require.Equal(t, mods, filtered)
	})

	t.Run("current module loads all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"currentModule"})
		require.Equal(t, mods, filtered)
	})

	t.Run("core-only query loads none", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"container", "version"})
		require.Empty(t, filtered)
	})

	t.Run("current workspace loads none (resolvers load on demand)", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"currentWorkspace"})
		require.Empty(t, filtered)
	})

	t.Run("already-served root field loads none", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"my-mod": {}}
		filtered := filterPendingWorkspaceModulesForRootFields(mods, served, []string{"myMod"})
		require.Empty(t, filtered)
	})

	t.Run("served field combined with pending field loads only pending", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"my-mod": {}}
		filtered := filterPendingWorkspaceModulesForRootFields(mods, served, []string{"myMod", "foo"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("env loads all (resolver snapshots served deps)", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"env"})
		require.Equal(t, mods, filtered)
	})

	t.Run("unrecognized loadFromID field loads all", func(t *testing.T) {
		t.Parallel()

		// The type name in load<Type>FromID needn't embed the module name, so
		// only a full load can guarantee the field exists.
		filtered := filterPendingWorkspaceModulesForRootFields(mods, nil, []string{"loadSomethingFromID"})
		require.Equal(t, mods, filtered)
	})
}

func TestFilterPendingWorkspaceModulesForScopedRootFields(t *testing.T) {
	t.Parallel()

	foo := pendingModule{Kind: moduleLoadKindAmbient, Name: "foo"}
	barBaz := pendingModule{Kind: moduleLoadKindAmbient, Name: "barBaz"}
	entry := pendingModule{Kind: moduleLoadKindAmbient, Name: "entry", Entrypoint: true}
	mods := []pendingModule{foo, barBaz, entry}

	t.Run("no scope delegates to root-field demand", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"currentTypeDefs"}, "", false)
		require.False(t, applied)
		require.Equal(t, mods, selected)
	})

	t.Run("scoped typedefs loads target plus entrypoint", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"currentTypeDefs"}, "foo", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{foo, entry}, selected)
	})

	t.Run("kebab-case token matches declared module name", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"currentTypeDefs"}, "bar-baz", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{barBaz, entry}, selected)
	})

	t.Run("unknown token loads pending entrypoint alone", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"currentTypeDefs"}, "greet", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{entry}, selected)
	})

	t.Run("unknown token without entrypoint loads all", func(t *testing.T) {
		t.Parallel()

		noEntry := []pendingModule{foo, barBaz}
		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(noEntry, nil, []string{"currentTypeDefs"}, "greet", false)
		require.True(t, applied)
		require.Equal(t, noEntry, selected)
	})

	t.Run("another full-schema field loads all without consuming", func(t *testing.T) {
		t.Parallel()

		for _, field := range []string{"env", "__schema", "currentModule"} {
			selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"currentTypeDefs", field}, "foo", false)
			require.False(t, applied, field)
			require.Equal(t, mods, selected, field)
		}
	})

	t.Run("no typedefs field delegates without consuming", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"foo"}, "barBaz", false)
		require.False(t, applied)
		require.Equal(t, []pendingModule{foo}, selected)
	})

	t.Run("typedefs plus module field unions both demands", func(t *testing.T) {
		t.Parallel()

		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, nil, []string{"currentTypeDefs", "foo"}, "bar-baz", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{foo, barBaz, entry}, selected)
	})

	t.Run("served target contributes nothing to load", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"my-mod": {}}
		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(mods, served, []string{"currentTypeDefs"}, "myMod", false)
		require.True(t, applied)
		require.Equal(t, []pendingModule{entry}, selected)
	})

	t.Run("unknown token with served entrypoint loads no siblings", func(t *testing.T) {
		t.Parallel()

		// A prior selective request may have loaded the entrypoint without
		// consuming the scope. The later typedefs request is then already
		// satisfied by that served entrypoint and must not fall back to every
		// still-pending workspace module.
		served := map[string]struct{}{"entry": {}}
		pending := []pendingModule{foo, barBaz}
		selected, applied := filterPendingWorkspaceModulesForScopedRootFields(pending, served, []string{"currentTypeDefs"}, "greet", true)
		require.True(t, applied)
		require.Empty(t, selected)
	})
}

func TestEnsureRequestModulesLoadedConsumesScopeBeforeUnlock(t *testing.T) {
	client := &daggerClient{
		clientID: "client",
		clientMetadata: &engine.ClientMetadata{
			WorkspaceModuleScope: "good",
		},
		pendingModules: []pendingModule{
			{Kind: moduleLoadKindAmbient, Name: "bad"},
		},
		servedWorkspaceModuleNames: map[string]struct{}{"good": {}},
	}
	req := httptest.NewRequest(http.MethodPost, engine.QueryEndpoint, strings.NewReader(`{"query":"{ currentTypeDefs { name } }"}`))
	req.Header.Set("Content-Type", "application/json")

	postLoad := make(chan struct{})
	resume := make(chan struct{})
	var resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	t.Cleanup(release)

	loadDone := make(chan error, 1)
	go func() {
		loadDone <- (&Server{}).ensureRequestModulesLoadedWithPostLoad(context.Background(), client, req, func() {
			close(postLoad)
			<-resume
		})
	}()

	select {
	case <-postLoad:
	case <-time.After(10 * time.Second):
		t.Fatal("module loading did not return")
	}

	client.modulesMu.Lock()
	observedScope := client.pendingWorkspaceModuleScopeLocked()
	client.modulesMu.Unlock()

	release()
	require.Empty(t, observedScope, "the one-shot scope was visible after the successful load released modulesMu")
	select {
	case err := <-loadDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("request module loading did not finish")
	}
}

func TestFilterPendingWorkspaceModulesBySelectorInclude(t *testing.T) {
	t.Parallel()

	mods := []pendingModule{
		{Kind: moduleLoadKindAmbient, Name: "go-sdk"},
		{Kind: moduleLoadKindAmbient, Name: "rust-sdk"},
		{Kind: moduleLoadKindAmbient, Name: "php-sdk"},
	}

	t.Run("module:generator selects only that module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-sdk:generate"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("module:item works for checks and services too", func(t *testing.T) {
		t.Parallel()

		// The module-name resolution is identical across generate/check/up: the
		// segment before ':' is the module name regardless of the item kind.
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"rust-sdk:lint", "php-sdk:web"})
		require.Equal(t, []pendingModule{mods[1], mods[2]}, filtered)
	})

	t.Run("bare module name selects only that module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-sdk"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("multiple patterns select each named module", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-sdk", "php-sdk:api"})
		require.Equal(t, []pendingModule{mods[0], mods[2]}, filtered)
	})

	t.Run("bare token not matching a module selects all", func(t *testing.T) {
		t.Parallel()

		// e.g. an item served by the entrypoint module.
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"generate"})
		require.Equal(t, mods, filtered)
	})

	t.Run("module:item not matching a module selects all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"typo-sdk:generate"})
		require.Equal(t, mods, filtered)
	})

	t.Run("empty include selects all", func(t *testing.T) {
		t.Parallel()

		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, nil)
		require.Equal(t, mods, filtered)
	})

	t.Run("already-served module is recognized and selects nothing", func(t *testing.T) {
		t.Parallel()

		// A re-evaluated selector (e.g. loading a GeneratorGroup from its ID
		// on a later request) names a module that already loaded; it must not
		// fall back to loading everything.
		served := map[string]struct{}{"dang-sdk": {}}
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, served, []string{"dang-sdk"})
		require.Empty(t, filtered)
	})

	t.Run("served and pending patterns select only the pending module", func(t *testing.T) {
		t.Parallel()

		served := map[string]struct{}{"dang-sdk": {}}
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, served, []string{"dang-sdk:generate", "go-sdk"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("camelCase pattern selects the kebab-case module", func(t *testing.T) {
		t.Parallel()

		// Name matching is kebab-normalized on both sides, like the include
		// matchers the selector resolvers use (ModTreePath.Glob/CliCase).
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"goSdk:generate"})
		require.Equal(t, []pendingModule{mods[0]}, filtered)
	})

	t.Run("kebab-case pattern selects the camelCase module", func(t *testing.T) {
		t.Parallel()

		// The CLI presents module commands in kebab-case, so a module declared
		// as "myMod" (or "mod1", which kebab-cases to "mod-1") is targeted by
		// its kebab-case name.
		camelMods := []pendingModule{
			{Kind: moduleLoadKindAmbient, Name: "myMod"},
			{Kind: moduleLoadKindAmbient, Name: "mod1"},
			{Kind: moduleLoadKindAmbient, Name: "other"},
		}
		filtered := filterPendingWorkspaceModulesBySelectorInclude(camelMods, nil, []string{"my-mod", "mod-1:generate"})
		require.Equal(t, []pendingModule{camelMods[0], camelMods[1]}, filtered)
	})

	t.Run("glob pattern selects all", func(t *testing.T) {
		t.Parallel()

		// Glob metacharacters survive normalization and never equal a module
		// name, so glob patterns conservatively load everything.
		filtered := filterPendingWorkspaceModulesBySelectorInclude(mods, nil, []string{"go-*"})
		require.Equal(t, mods, filtered)
	})
}

func TestWorkspaceConfigPendingModules(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{
		Root:       "/repo",
		Cwd:        ".",
		ConfigFile: workspace.ConfigFileName,
		LockFile:   filepath.Join(workspace.LockDirName, workspace.LockFileName),
	}
	resolveLocalRef := func(_ *workspace.Workspace, relPath string) string {
		return filepath.Join("/resolved", relPath)
	}

	pending := workspaceConfigPendingModules(ws, &workspace.Config{
		DefaultsFromDotEnv: true,
		Modules: map[string]workspace.ModuleEntry{
			"zeta": {
				Source:     "github.com/acme/zeta@main",
				Entrypoint: true,
				Settings:   map[string]any{"message": "hello"},
			},
			"alpha": {
				Source:            "modules/alpha",
				LegacyDefaultPath: true,
			},
		},
	}, resolveLocalRef)
	require.Len(t, pending, 2)

	require.Equal(t, "alpha", pending[0].Name)
	require.Equal(t, "/resolved/modules/alpha", pending[0].Ref)
	require.Empty(t, pending[0].RefPin)
	require.False(t, pending[0].Entrypoint)
	require.True(t, pending[0].DisableFindUp)
	require.True(t, pending[0].LegacyDefaultPath)
	require.Equal(t, "/resolved", pending[0].DefaultPathContextSourceRef)
	require.True(t, pending[0].DefaultsFromDotEnv)

	require.Equal(t, "zeta", pending[1].Name)
	require.Equal(t, "github.com/acme/zeta@main", pending[1].Ref)
	require.Empty(t, pending[1].RefPin)
	require.True(t, pending[1].Entrypoint)
	require.True(t, pending[1].DisableFindUp)
	require.False(t, pending[1].LegacyDefaultPath)
	require.Empty(t, pending[1].DefaultPathContextSourceRef)
	require.True(t, pending[1].DefaultsFromDotEnv)
	require.Equal(t, map[string]any{"message": "hello"}, pending[1].ConfigDefaults)
}

// TestModuleResolutionFromSubdirectory verifies that module source paths from
// dagger.json are resolved relative to the config file location, not the
// client's working directory. When a client connects from sdk/go/, a module
// with source "modules/changelog" should resolve to /repo/modules/changelog,
// not /repo/sdk/go/modules/changelog.
func TestModuleResolutionFromSubdirectory(t *testing.T) {
	t.Parallel()

	// Filesystem layout:
	//   /repo/.git                  (git root)
	//   /repo/dagger.json           (config declaring a module)
	//   /repo/sdk/go/               (client CWD)

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	// The "toolchains" field is the current config mechanism for declaring
	// workspace modules in dagger.json.
	daggerJSON := `{
		"name": "myproject",
		"toolchains": [
			{"name": "changelog", "source": "modules/changelog"}
		]
	}`

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(daggerJSON), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		return filepath.Join(ws.Root, relPath)
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/sdk/go", // CWD is a subdirectory
		resolveLocalRef,
		nil,
		true, // isLocal
	)
	require.NoError(t, err)
	require.Equal(t, "sdk/go", client.workspace.Cwd)

	// Module source must resolve relative to dagger.json (/repo),
	// not relative to CWD (/repo/sdk/go).
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, "/repo/modules/changelog", client.pendingModules[0].Ref)
	require.Equal(t, "changelog", client.pendingModules[0].Name)
}

func TestDetectAndLoadWorkspaceIgnoresCompatFallbackWhenConfigExists(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":                      true,
		"/repo/dagger.toml":               true,
		"/repo/mymod/dagger.json":         true,
		"/repo/modules/local":             true,
		"/repo/modules/local/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		switch filepath.Clean(path) {
		case "/repo/dagger.toml":
			return []byte(`[modules.dev]
source = "github.com/acme/dev@main"
entrypoint = true

[modules.local]
source = "modules/local"
`), nil
		case "/repo/mymod/dagger.json":
			return []byte(`{"name":"mymod","sdk":{"source":"go"}}`), nil
		default:
			return nil, os.ErrNotExist
		}
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Equal(t, "mymod", client.workspace.Cwd)
	require.Equal(t, workspace.ConfigFileName, client.workspace.ConfigFile)

	require.Len(t, client.pendingModules, 2)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "dev", client.pendingModules[0].Name)
	require.Equal(t, "github.com/acme/dev@main", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)

	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[1].Kind)
	require.Equal(t, "local", client.pendingModules[1].Name)
	require.Equal(t, "/repo/modules/local", client.pendingModules[1].Ref)
	require.False(t, client.pendingModules[1].Entrypoint)
}

func TestDetectAndLoadWorkspaceLoadsPlainModuleCompatWithoutConfig(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":              true,
		"/repo/mymod/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/mymod/dagger.json" {
			return []byte(`{"name":"mymod","sdk":{"source":"go"}}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.Empty(t, client.workspace.ConfigFile)
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "mymod", client.pendingModules[0].Name)
	require.Equal(t, "/repo/mymod", client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestDetectAndLoadWorkspaceKeepsCompatFallbackForExplicitExtraModule(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(`{"name":"ambient","toolchains":[{"name":"tool","source":"./tool"}]}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	extra := []engine.ExtraModule{{
		Ref:        "/repo/explicit",
		Entrypoint: true,
	}}
	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			ExtraModules: extra,
		},
		pendingExtraModules: extra,
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.NotNil(t, client.workspace.CompatWorkspace())
	require.Empty(t, client.pendingModules)
	require.Equal(t, extra, client.pendingExtraModules)
}

func TestDetectAndLoadWorkspaceCreatesRootlessWorkspaceWithoutInferringModule(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/tmp/mymod/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/tmp/mymod/dagger.json" {
			return []byte(`{"name":"mymod"}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/tmp/mymod",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.Equal(t, "/tmp/mymod", client.workspace.HostPath())
	_, ok := client.workspace.Source().(*core.WorkspaceSourceRootlessLocal)
	require.True(t, ok)
	require.Empty(t, client.pendingModules)
}

func TestRemoteWorkspaceCwdUsesDetectionStart(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"dagger.toml": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "dagger.toml" {
			return []byte("# workspace\n"), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
		return core.GitRefString("github.com/acme/repo", subPath, "main")
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata:       &engine.ClientMetadata{},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		statFS,
		readFile,
		"subdir",
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress("github.com/acme/repo", ws.Cwd, "main")
		},
		false,
		dagql.ObjectResult[*core.Directory]{},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "subdir", client.workspace.Cwd)
	require.Equal(t, "github.com/acme/repo/subdir@main", client.workspace.Address)
	require.Equal(t, workspace.ConfigFileName, client.workspace.ConfigFile)
}

func TestRemoteWorkspaceLoadsPlainModuleCompatFromCWD(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"subdir/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "subdir/dagger.json" {
			return []byte(`{"name":"remote-mod","sdk":{"source":"go"}}`), nil
		}
		return nil, os.ErrNotExist
	}

	resolveLocalRef := func(ws *workspace.Workspace, relPath string) string {
		subPath := filepath.Join(ws.Root, relPath)
		return core.GitRefString("github.com/acme/repo", subPath, "main")
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata: &engine.ClientMetadata{
			LoadWorkspaceModules: true,
		},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspaceWithRootfs(ctx, client,
		statFS,
		readFile,
		"subdir/child",
		resolveLocalRef,
		func(ws *workspace.Workspace) string {
			return remoteWorkspaceAddress("github.com/acme/repo", ws.Cwd, "main")
		},
		false,
		dagql.ObjectResult[*core.Directory]{},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("subdir", "child"), client.workspace.Cwd)
	require.Len(t, client.pendingModules, 1)
	require.Equal(t, moduleLoadKindAmbient, client.pendingModules[0].Kind)
	require.Equal(t, "remote-mod", client.pendingModules[0].Name)
	require.Equal(t, core.GitRefString("github.com/acme/repo", "subdir", "main"), client.pendingModules[0].Ref)
	require.True(t, client.pendingModules[0].Entrypoint)
}

func TestDetectAndLoadWorkspaceDoesNotLoadModulesByDefault(t *testing.T) {
	t.Parallel()

	existingFiles := map[string]bool{
		"/repo/.git":        true,
		"/repo/dagger.json": true,
	}

	statFS := core.StatFSFunc(func(_ context.Context, path string) (string, *core.Stat, error) {
		path = filepath.Clean(path)
		if existingFiles[path] {
			return filepath.Dir(path), &core.Stat{
				Name: filepath.Base(path),
			}, nil
		}
		return "", nil, os.ErrNotExist
	})

	readFile := func(_ context.Context, path string) ([]byte, error) {
		if filepath.Clean(path) == "/repo/dagger.json" {
			return []byte(`{"name":"myproject","toolchains":[{"name":"changelog","source":"modules/changelog"}]}`), nil
		}
		return nil, os.ErrNotExist
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "test-client",
	})

	client := &daggerClient{
		pendingWorkspaceLoad: true,
		clientMetadata:       &engine.ClientMetadata{},
	}

	srv := &Server{}
	err := srv.detectAndLoadWorkspace(ctx, client,
		statFS,
		readFile,
		"/repo/sdk/go",
		func(ws *workspace.Workspace, relPath string) string {
			return filepath.Join(ws.Root, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
	require.NotNil(t, client.workspace.CompatWorkspace())
	require.Empty(t, client.pendingModules)
}

func TestIsSameModuleReference(t *testing.T) {
	t.Parallel()

	local := func(contextPath, rootSubpath, sourceSubpath string) *core.ModuleSource {
		return &core.ModuleSource{
			Kind:              core.ModuleSourceKindLocal,
			Local:             &core.LocalModuleSource{ContextDirectoryPath: contextPath},
			SourceRootSubpath: rootSubpath,
			SourceSubpath:     sourceSubpath,
		}
	}

	t.Run("same local source root and pin", func(t *testing.T) {
		t.Parallel()
		a := local("/work/mod", ".", ".")
		b := local("/work/mod", ".", ".")
		require.True(t, isSameModuleReference(a, b))
	})

	t.Run("different local source", func(t *testing.T) {
		t.Parallel()
		a := local("/work/mod-a", ".", ".")
		b := local("/work/mod-b", ".", ".")
		require.False(t, isSameModuleReference(a, b))
	})

	t.Run("same module through different local refs", func(t *testing.T) {
		t.Parallel()
		// a points at the workspace root where dagger.json has sourceSubpath
		// ".dagger/modules/dagger-dev". b points directly at that module dir.
		a := local("/root/src/dagger", ".", ".dagger/modules/dagger-dev")
		b := local("/root/src/dagger/.dagger/modules/dagger-dev", ".", ".")
		require.True(t, isSameModuleReference(a, b))
	})
}

func TestEnsureWorkspaceLoadedInheritsParentWorkspace(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	bound := &core.Workspace{
		ClientID: "parent-client",
	}

	parent := &daggerClient{
		workspace: bound,
	}
	child := &daggerClient{
		parents: []*daggerClient{parent},
	}

	require.NoError(t, srv.ensureWorkspaceLoaded(context.Background(), child))
	require.Same(t, bound, child.workspace)
}

func TestEnsureWorkspaceLoadedKeepsExistingWorkspaceBinding(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	existing := &core.Workspace{
		ClientID: "child-client",
	}
	parentBound := &core.Workspace{
		ClientID: "parent-client",
	}

	parent := &daggerClient{
		workspace: parentBound,
	}
	child := &daggerClient{
		workspace: existing,
		parents:   []*daggerClient{parent},
	}

	require.NoError(t, srv.ensureWorkspaceLoaded(context.Background(), child))
	require.Same(t, existing, child.workspace)
}

func TestResolveHostServiceCallerFallsBackToParentForSyntheticNestedClient(t *testing.T) {
	t.Parallel()

	parentCaller := &fakeSessionCaller{id: "parent"}
	parent := &daggerClient{clientID: "parent"}
	parent.getHostServiceCaller = func(ctx context.Context, id string) (engineutil.SessionCaller, error) {
		require.Equal(t, "parent", id)
		return parentCaller, nil
	}

	child := &daggerClient{
		clientID:                 "child",
		hostServiceProxyClientID: "parent",
		parents:                  []*daggerClient{parent},
	}

	child.daggerSession = &daggerSession{attachables: newSessionAttachableManager()}

	caller, err := child.resolveHostServiceCaller(context.Background(), "child")
	require.NoError(t, err)
	require.Same(t, parentCaller, caller)
}

func TestResolveHostServiceCallerPrefersCurrentClientAttachable(t *testing.T) {
	t.Parallel()

	currentCaller := &sessionAttachableCaller{
		ctx:       context.Background(),
		supported: map[string]struct{}{},
	}
	parent := &daggerClient{clientID: "parent"}
	parent.getHostServiceCaller = func(context.Context, string) (engineutil.SessionCaller, error) {
		t.Fatal("unexpected parent fallback")
		return nil, nil
	}
	attachables := newSessionAttachableManager()
	attachables.callers["child"] = currentCaller

	child := &daggerClient{
		clientID:                 "child",
		hostServiceProxyClientID: "parent",
		parents:                  []*daggerClient{parent},
		daggerSession:            &daggerSession{attachables: attachables},
	}

	caller, err := child.resolveHostServiceCaller(context.Background(), "child")
	require.NoError(t, err)
	require.Same(t, currentCaller, caller)
}

func TestResolveHostServiceCallerUsesBlockingLookupForOtherClients(t *testing.T) {
	t.Parallel()

	otherCaller := &fakeSessionCaller{id: "other"}
	child := &daggerClient{clientID: "child"}
	child.getClientCaller = func(ctx context.Context, id string) (engineutil.SessionCaller, error) {
		require.Equal(t, "other", id)
		return otherCaller, nil
	}

	caller, err := child.resolveHostServiceCaller(context.Background(), "other")
	require.NoError(t, err)
	require.Same(t, otherCaller, caller)
}

func TestWorkspaceBindingMode(t *testing.T) {
	t.Parallel()

	t.Run("declared workspace takes precedence", func(t *testing.T) {
		t.Parallel()

		client := &daggerClient{
			pendingWorkspaceLoad: false,
			clientMetadata: &engine.ClientMetadata{
				Workspace: stringPtr("github.com/dagger/dagger@main"),
			},
		}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingDeclared, mode)
		require.Equal(t, "github.com/dagger/dagger@main", workspaceRef)
	})

	t.Run("non-module defaults to host detection", func(t *testing.T) {
		t.Parallel()

		client := &daggerClient{
			pendingWorkspaceLoad: true,
			clientMetadata:       &engine.ClientMetadata{},
		}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingDetectHost, mode)
		require.Equal(t, "", workspaceRef)
	})

	t.Run("module defaults to inheritance", func(t *testing.T) {
		t.Parallel()

		client := &daggerClient{
			pendingWorkspaceLoad: false,
			clientMetadata:       &engine.ClientMetadata{},
		}

		mode, workspaceRef := workspaceBindingMode(client)
		require.Equal(t, workspaceBindingInherit, mode)
		require.Equal(t, "", workspaceRef)
	})
}

func TestBuildCoreWorkspaceIncludesConfigState(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "main-client",
	})

	t.Run("workspace with config", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:       "/repo",
			HasGitRoot: true,
			Cwd:        filepath.Join("services", "payment", "src"),
			ConfigFile: filepath.Join("services", "payment", workspace.ConfigFileName),
			LockFile:   filepath.Join("services", "payment", workspace.LockDirName, workspace.LockFileName),
		}, true, dagql.ObjectResult[*core.Directory]{}, nil, "")
		require.NoError(t, err)
		require.Equal(t, "file:///repo/services/payment/src", ws.Address)
		require.Equal(t, filepath.Join("services", "payment", "src"), ws.Cwd)
		require.Equal(t, filepath.Join("services", "payment", workspace.ConfigFileName), ws.ConfigFile)
		require.Equal(t, filepath.Join("services", "payment", workspace.LockDirName, workspace.LockFileName), ws.LockFile)
		require.Equal(t, "/repo", ws.HostPath())
	})

	t.Run("workspace without config", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:       "/repo",
			HasGitRoot: true,
			Cwd:        ".",
			LockFile:   filepath.Join(workspace.LockDirName, workspace.LockFileName),
		}, true, dagql.ObjectResult[*core.Directory]{}, nil, "")
		require.NoError(t, err)
		require.Empty(t, ws.ConfigFile)
		require.Equal(t, filepath.Join(workspace.LockDirName, workspace.LockFileName), ws.LockFile)
	})

	t.Run("local boundary without Git is rootless", func(t *testing.T) {
		t.Parallel()

		ws, err := srv.buildCoreWorkspace(ctx, nil, &workspace.Workspace{
			Root:     "/repo",
			Cwd:      ".",
			LockFile: workspace.LockFileName,
		}, true, dagql.ObjectResult[*core.Directory]{}, nil, "")
		require.NoError(t, err)
		require.Equal(t, "/repo", ws.HostPath())
		_, ok := ws.Source().(*core.WorkspaceSourceRootlessLocal)
		require.True(t, ok)
		_, err = ws.ExportHostPath()
		require.ErrorContains(t, err, "requires a local Git workspace")
	})
}

func TestNestedClientMetadataForRequest(t *testing.T) {
	t.Parallel()

	baseMetadata := func() *engine.ClientMetadata {
		return &engine.ClientMetadata{
			ClientID:          "nested-client",
			ClientSecretToken: "secret",
			SessionID:         "session",
			ClientHostname:    "nested-host",
			ClientStableID:    "stable",
			ClientVersion:     "",
			Labels: map[string]string{
				"ignored": "true",
			},
			SSHAuthSocketPath: "/tmp/ssh.sock",
			AllowedLLMModules: []string{"parent"},
			ExtraModules: []engine.ExtraModule{{
				Ref: "github.com/dagger/base-extra",
			}},
			LoadWorkspaceModules:  true,
			EagerRuntime:          true,
			LockMode:              string(workspace.LockModeFrozen),
			Workspace:             stringPtr("github.com/dagger/base@main"),
			WorkspaceEnv:          stringPtr("parent-ci"),
			WorkspaceModuleScope:  "parent-scope",
			UseRecipeIDsByDefault: true,
		}
	}

	t.Run("inherits live nested client identity and policy without forwarded metadata", func(t *testing.T) {
		t.Parallel()

		base := baseMetadata()
		md := nestedClientMetadataForRequest(http.Header{}, base)

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "secret", md.ClientSecretToken)
		require.Equal(t, "session", md.SessionID)
		require.Equal(t, "nested-host", md.ClientHostname)
		require.Equal(t, "stable", md.ClientStableID)
		require.Equal(t, engine.Version, md.ClientVersion)
		require.Empty(t, md.Labels)
		require.Equal(t, "/tmp/ssh.sock", md.SSHAuthSocketPath)
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeFrozen), md.LockMode)
		require.Empty(t, md.ExtraModules)
		require.False(t, md.LoadWorkspaceModules)
		require.False(t, md.EagerRuntime)
		require.Nil(t, md.Workspace)
		require.Nil(t, md.WorkspaceEnv)
		require.Empty(t, md.WorkspaceModuleScope)
		require.True(t, md.UseRecipeIDsByDefault)

		base.AllowedLLMModules[0] = "mutated"
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
	})

	t.Run("overlays request-scoped forwarded metadata", func(t *testing.T) {
		t.Parallel()

		workspaceRef := "github.com/dagger/dagger@main"
		workspaceEnv := "ci"
		forwarded := engine.ClientMetadata{
			ClientID:          "forwarded-client",
			ClientSecretToken: "forwarded-secret",
			SessionID:         "forwarded-session",
			ClientHostname:    "forwarded-host",
			ClientStableID:    "forwarded-stable",
			ClientVersion:     "v-test",
			Labels: map[string]string{
				"forwarded": "ignored",
			},
			SSHAuthSocketPath: "/tmp/forwarded-ssh.sock",
			AllowedLLMModules: []string{"child"},
			ExtraModules: []engine.ExtraModule{{
				Ref:        "github.com/dagger/mod",
				Entrypoint: true,
			}},
			LoadWorkspaceModules:           true,
			EagerRuntime:                   true,
			SuppressCompatWorkspaceWarning: true,
			LockMode:                       string(workspace.LockModeLive),
			Workspace:                      &workspaceRef,
			WorkspaceEnv:                   &workspaceEnv,
			WorkspaceModuleScope:           "good-mod",
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), baseMetadata())

		require.Equal(t, "nested-client", md.ClientID)
		require.Equal(t, "secret", md.ClientSecretToken)
		require.Equal(t, "session", md.SessionID)
		require.Equal(t, "nested-host", md.ClientHostname)
		require.Equal(t, "stable", md.ClientStableID)
		require.Equal(t, "/tmp/ssh.sock", md.SSHAuthSocketPath)
		require.Empty(t, md.Labels)

		require.Equal(t, "v-test", md.ClientVersion)
		require.Equal(t, []string{"child"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeLive), md.LockMode)
		require.True(t, md.LoadWorkspaceModules)
		require.True(t, md.EagerRuntime)
		require.True(t, md.SuppressCompatWorkspaceWarning)
		require.Equal(t, "github.com/dagger/dagger@main", *md.Workspace)
		require.Equal(t, "ci", *md.WorkspaceEnv)
		require.Equal(t, "good-mod", md.WorkspaceModuleScope)
		require.Equal(t, []engine.ExtraModule{{
			Ref:        "github.com/dagger/mod",
			Entrypoint: true,
		}}, md.ExtraModules)
		require.True(t, md.UseRecipeIDsByDefault)
	})

	t.Run("keeps parent lock mode when forwarded metadata omits it", func(t *testing.T) {
		t.Parallel()

		forwarded := engine.ClientMetadata{
			ClientVersion:     "v-test",
			AllowedLLMModules: []string{"child"},
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), baseMetadata())

		require.Equal(t, "v-test", md.ClientVersion)
		require.Equal(t, []string{"child"}, md.AllowedLLMModules)
		require.Equal(t, string(workspace.LockModeFrozen), md.LockMode)
		require.Nil(t, md.WorkspaceEnv)
		require.True(t, md.UseRecipeIDsByDefault)
	})

	t.Run("does not accept internal recipe ID default from forwarded metadata", func(t *testing.T) {
		t.Parallel()

		base := baseMetadata()
		base.UseRecipeIDsByDefault = false
		forwarded := engine.ClientMetadata{
			ClientVersion:         "v-test",
			UseRecipeIDsByDefault: true,
		}

		md := nestedClientMetadataForRequest(forwarded.AppendToHTTPHeaders(http.Header{}), base)

		require.False(t, md.UseRecipeIDsByDefault)
	})
}

func TestLocalWorkspaceAddress(t *testing.T) {
	t.Parallel()

	require.Equal(t, "file:///repo", localWorkspaceAddress("/repo", "."))
	require.Equal(t, "file:///repo/services/payment", localWorkspaceAddress("/repo", "services/payment"))
}

func TestRemoteWorkspaceAddress(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://github.com/dagger/dagger@main", remoteWorkspaceAddress("https://github.com/dagger/dagger", ".", "main"))
	require.Equal(t, "https://github.com/dagger/dagger/services/payment@main", remoteWorkspaceAddress("https://github.com/dagger/dagger", "services/payment", "main"))
}

func TestParseWorkspaceRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("supports address fragment ref", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.cloneRef)
		require.Equal(t, "main", ref.version)
		require.Equal(t, ".", ref.workspaceSubdir)
	})

	t.Run("supports address fragment ref and subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main:toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.cloneRef)
		require.Equal(t, "main", ref.version)
		require.Equal(t, "toolchains/changelog", ref.workspaceSubdir)
	})

	t.Run("supports legacy at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "github.com/dagger/dagger/toolchains/changelog@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.version)
		require.Equal(t, "toolchains/changelog", ref.workspaceSubdir)
	})

	t.Run("preserves legacy https at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.version)
		require.Equal(t, ".", ref.workspaceSubdir)
	})
}

func TestGatherModuleLoadRequests(t *testing.T) {
	t.Parallel()

	loads := gatherModuleLoadRequests(
		[]pendingModule{
			{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/a", Name: "a"},
			{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/b", Name: "b"},
		},
		[]engine.ExtraModule{
			{Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true},
			{Ref: "github.com/acme/extra2", Name: "extra2"},
		},
	)

	require.Len(t, loads, 4)
	require.Equal(t, moduleLoadKindAmbient, loads[0].mod.Kind)
	require.Equal(t, moduleLoadKindAmbient, loads[1].mod.Kind)
	require.Equal(t, moduleLoadKindExtra, loads[2].mod.Kind)
	require.Equal(t, moduleLoadKindExtra, loads[3].mod.Kind)

	require.Equal(t, "github.com/acme/a", loads[0].mod.Ref)
	require.Equal(t, "github.com/acme/b", loads[1].mod.Ref)
	require.Equal(t, "github.com/acme/extra1", loads[2].mod.Ref)
	require.Equal(t, "github.com/acme/extra2", loads[3].mod.Ref)
	require.True(t, loads[2].mod.Entrypoint)
}

func TestModuleLoadParallelism(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, moduleLoadParallelism(0))
	require.Equal(t, 1, moduleLoadParallelism(1))
	require.Equal(t, 3, moduleLoadParallelism(3))
	require.Equal(t, maxParallelModuleResolves, moduleLoadParallelism(maxParallelModuleResolves+4))
}

func TestModuleLoadErr(t *testing.T) {
	t.Parallel()

	err := errors.New("boom")

	normal := moduleLoadErr(moduleLoadRequest{mod: pendingModule{Ref: "github.com/acme/mod"}}, err)
	require.ErrorContains(t, normal, `loading module "github.com/acme/mod": boom`)

	extra := moduleLoadErr(moduleLoadRequest{
		mod: pendingModule{
			Kind: moduleLoadKindExtra,
			Ref:  "github.com/acme/extra",
		},
	}, err)
	require.ErrorContains(t, extra, `loading extra module "github.com/acme/extra": boom`)
}

func TestDedupeResolvedModuleLoads(t *testing.T) {
	t.Parallel()

	loads := []moduleLoadRequest{
		{
			mod: pendingModule{
				Kind:       moduleLoadKindAmbient,
				Ref:        "github.com/acme/app",
				Name:       "app",
				Entrypoint: false,
			},
		},
		{
			mod: pendingModule{
				Kind:       moduleLoadKindExtra,
				Ref:        "github.com/acme/app",
				Name:       "app",
				Entrypoint: true,
			},
		},
		{
			mod: pendingModule{
				Kind:       moduleLoadKindAmbient,
				Ref:        "github.com/acme/other",
				Name:       "other",
				Entrypoint: false,
			},
		},
	}
	resolved := []resolvedModuleLoad{
		{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: false},
		{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
		{primary: sessionTestModuleResult(t, "other"), primaryEntrypoint: false},
	}

	dedupLoads, dedupResolved := dedupeResolvedModuleLoads(loads, resolved)
	require.Len(t, dedupLoads, 2)

	require.Equal(t, moduleLoadKindExtra, dedupLoads[0].mod.Kind)
	require.True(t, dedupResolved[0].primaryEntrypoint)

	require.Equal(t, moduleLoadKindAmbient, dedupLoads[1].mod.Kind)
	require.False(t, dedupResolved[1].primaryEntrypoint)
}

func TestArbitrateResolvedModuleLoads(t *testing.T) {
	t.Parallel()

	t.Run("extra beats ambient", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra", Name: "extra", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "extra"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.NoError(t, err)
		require.False(t, resolved[0].primaryEntrypoint)
		require.True(t, resolved[1].primaryEntrypoint)
	})

	t.Run("multiple ambient entrypoints are invalid", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/app", Name: "app", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindAmbient, Ref: "github.com/acme/other", Name: "other", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "app"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "other"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.EqualError(t, err, "invalid workspace configuration: multiple distinct ambient entrypoint modules: app, other")
	})

	t.Run("multiple extra entrypoints are invalid", func(t *testing.T) {
		t.Parallel()

		loads := []moduleLoadRequest{
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true}},
			{mod: pendingModule{Kind: moduleLoadKindExtra, Ref: "github.com/acme/extra2", Name: "extra2", Entrypoint: true}},
		}
		resolved := []resolvedModuleLoad{
			{primary: sessionTestModuleResult(t, "extra1"), primaryEntrypoint: true},
			{primary: sessionTestModuleResult(t, "extra2"), primaryEntrypoint: true},
		}

		err := arbitrateResolvedModuleLoads(loads, resolved)
		require.EqualError(t, err, "invalid extra-module request: multiple distinct extra-module entrypoints: extra1, extra2")
	})
}

func TestNormalizeWorkspaceRemoteSubdir(t *testing.T) {
	t.Parallel()

	t.Run("empty becomes dot", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("absolute gets normalized to relative", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("/toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "toolchains/changelog", got)
	})

	t.Run("rejects escaping paths", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeWorkspaceRemoteSubdir("../outside")
		require.ErrorContains(t, err, "outside repository")
	})
}

func TestReadWorkspaceLockStateReadsLegacyLockFallback(t *testing.T) {
	t.Parallel()

	legacy := workspace.NewLock()
	require.NoError(t, legacy.SetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"}, workspace.LookupResult{
		Value:  "sha256:deadbeef",
		Policy: workspace.PolicyPin,
	}))
	legacyBytes, err := legacy.Marshal()
	require.NoError(t, err)

	ws := &core.Workspace{
		ConfigFile: "dagger.toml",
		LockFile:   "dagger.lock",
	}
	ws.SetHostPath("/repo")

	lock, err := readWorkspaceLockState(t.Context(), fakeWorkspaceLockStateReader{
		files: map[string][]byte{
			filepath.Join("/repo", ".dagger", "lock"): legacyBytes,
		},
	}, ws)
	require.NoError(t, err)

	got, ok, err := lock.GetLookup("", "container.from", []any{"alpine:latest", "linux/amd64"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, workspace.LookupResult{Value: "sha256:deadbeef", Policy: workspace.PolicyPin}, got)
}

type fakeWorkspaceLockStateReader struct {
	files map[string][]byte
}

func (r fakeWorkspaceLockStateReader) ReadCallerHostFile(_ context.Context, path string) ([]byte, error) {
	if data, ok := r.files[path]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func stringPtr(v string) *string {
	return &v
}

func sessionTestModuleResult(t *testing.T, name string) dagql.ObjectResult[*core.Module] {
	t.Helper()

	dag, err := dagql.NewServer(t.Context(), &core.Module{})
	require.NoError(t, err)
	res, err := dagql.NewObjectResultForCall(
		&core.Module{NameField: name},
		dag,
		&dagql.ResultCall{SyntheticOp: "session-test-module-" + name},
	)
	require.NoError(t, err)
	return res
}
