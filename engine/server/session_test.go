package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/util/flightcontrol"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeSessionCaller struct {
	id   string
	conn *grpc.ClientConn
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

func TestPendingLegacyModule(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Root: "/repo", Path: "."}
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
		require.Nil(t, mod.ConfigDefaults)
	})
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
		return filepath.Join(ws.Root, ws.Path, relPath)
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

	// Module source must resolve relative to dagger.json (/repo),
	// not relative to CWD (/repo/sdk/go).
	require.Len(t, client.pendingModules, 2) // declared module + implicit module
	require.Equal(t, "/repo/modules/changelog", client.pendingModules[0].Ref)
	require.Equal(t, "changelog", client.pendingModules[0].Name)
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
			return filepath.Join(ws.Root, ws.Path, relPath)
		},
		nil,
		true,
	)
	require.NoError(t, err)
	require.NotNil(t, client.workspace)
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
		Path:     ".",
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
		Path:     ".",
		ClientID: "child-client",
	}
	parentBound := &core.Workspace{
		Path:     ".",
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
		require.True(t, md.UseRecipeIDsByDefault)

		base.AllowedLLMModules[0] = "mutated"
		require.Equal(t, []string{"parent"}, md.AllowedLLMModules)
	})

	t.Run("overlays request-scoped forwarded metadata", func(t *testing.T) {
		t.Parallel()

		workspaceRef := "github.com/dagger/dagger@main"
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
			LoadWorkspaceModules: true,
			EagerRuntime:         true,
			LockMode:             string(workspace.LockModeLive),
			Workspace:            &workspaceRef,
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
		require.Equal(t, "github.com/dagger/dagger@main", *md.Workspace)
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
			{Ref: "github.com/acme/a", Name: "a"},
			{Ref: "github.com/acme/b", Name: "b"},
		},
		[]engine.ExtraModule{
			{Ref: "github.com/acme/extra1", Name: "extra1", Entrypoint: true},
			{Ref: "github.com/acme/extra2", Name: "extra2"},
		},
	)

	require.Len(t, loads, 4)
	require.False(t, loads[0].extra)
	require.False(t, loads[1].extra)
	require.True(t, loads[2].extra)
	require.True(t, loads[3].extra)

	require.Equal(t, "github.com/acme/a", loads[0].mod.Ref)
	require.Equal(t, "github.com/acme/b", loads[1].mod.Ref)
	require.Equal(t, "github.com/acme/extra1", loads[2].mod.Ref)
	require.Equal(t, "github.com/acme/extra2", loads[3].mod.Ref)
	require.True(t, loads[2].mod.Entrypoint)
}

func TestModuleResolveParallelism(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, moduleResolveParallelism(0))
	require.Equal(t, 1, moduleResolveParallelism(1))
	require.Equal(t, 3, moduleResolveParallelism(3))
	require.Equal(t, maxParallelModuleResolves, moduleResolveParallelism(maxParallelModuleResolves+4))
}

func TestModuleLoadErr(t *testing.T) {
	t.Parallel()

	err := errors.New("boom")

	normal := moduleLoadErr(moduleLoadRequest{mod: pendingModule{Ref: "github.com/acme/mod"}}, err)
	require.ErrorContains(t, normal, `loading module "github.com/acme/mod": boom`)

	extra := moduleLoadErr(moduleLoadRequest{
		mod:   pendingModule{Ref: "github.com/acme/extra"},
		extra: true,
	}, err)
	require.ErrorContains(t, extra, `loading extra module "github.com/acme/extra": boom`)
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

func stringPtr(v string) *string {
	return &v
}
