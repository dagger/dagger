package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/h2c"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type fakeTunnelListenerHandle struct {
	completionMu sync.Mutex
	completed    bool
	result       error
	done         chan struct{}

	guardEntered chan struct{}
	guardRelease <-chan struct{}
	guardOnce    sync.Once
	waitReturned chan struct{}
	waitRelease  <-chan struct{}
	waitOnce     sync.Once

	closeErr   error
	closeCount int
	onClose    func()
}

func newFakeTunnelListenerHandle() *fakeTunnelListenerHandle {
	return &fakeTunnelListenerHandle{done: make(chan struct{})}
}

func waitCoreTunnelTest[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func (handle *fakeTunnelListenerHandle) finish(result error) {
	handle.completionMu.Lock()
	defer handle.completionMu.Unlock()
	if handle.completed {
		return
	}
	handle.completed = true
	handle.result = result
	close(handle.done)
}

func (handle *fakeTunnelListenerHandle) Close() error {
	handle.completionMu.Lock()
	handle.closeCount++
	onClose := handle.onClose
	handle.completionMu.Unlock()
	if onClose != nil {
		onClose()
	}
	handle.finish(nil)
	return handle.closeErr
}

func (handle *fakeTunnelListenerHandle) Wait() error {
	<-handle.done
	if handle.waitRelease != nil {
		<-handle.waitRelease
	}
	handle.completionMu.Lock()
	result := handle.result
	handle.completionMu.Unlock()
	if handle.waitReturned != nil {
		handle.waitOnce.Do(func() { close(handle.waitReturned) })
	}
	return result
}

func (handle *fakeTunnelListenerHandle) WithCompletionGuard(commit func() error) (bool, error) {
	handle.completionMu.Lock()
	defer handle.completionMu.Unlock()
	if handle.completed {
		return true, handle.result
	}
	if handle.guardEntered != nil {
		handle.guardOnce.Do(func() { close(handle.guardEntered) })
	}
	if handle.guardRelease != nil {
		<-handle.guardRelease
	}
	return false, commit()
}

func (handle *fakeTunnelListenerHandle) closes() int {
	handle.completionMu.Lock()
	defer handle.completionMu.Unlock()
	return handle.closeCount
}

func TestTunnelListenerRegistryCompletionRejectsPublication(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	handle := newFakeTunnelListenerHandle()
	require.NoError(t, registry.AddOrClose(handle))
	listenerErr := errors.New("listener failed")
	handle.finish(listenerErr)

	committed := false
	err := registry.Publish(func() { committed = true })
	require.ErrorIs(t, err, listenerErr)
	require.False(t, committed)
	firstCause, cleanupErr := registry.Result()
	require.ErrorIs(t, firstCause, listenerErr)
	require.NoError(t, cleanupErr)
}

func TestTunnelListenerRegistryPublicationHoldsCompletionGuard(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	guardEntered := make(chan struct{})
	guardRelease := make(chan struct{})
	handle := newFakeTunnelListenerHandle()
	handle.guardEntered = guardEntered
	handle.guardRelease = guardRelease
	require.NoError(t, registry.AddOrClose(handle))

	committed := make(chan struct{})
	publishResult := make(chan error, 1)
	go func() {
		publishResult <- registry.Publish(func() { close(committed) })
	}()
	waitCoreTunnelTest(t, guardEntered, "publication completion guard")
	listenerErr := errors.New("listener failed after publication began")
	finished := make(chan struct{})
	go func() {
		handle.finish(listenerErr)
		close(finished)
	}()
	close(guardRelease)

	require.NoError(t, waitCoreTunnelTest(t, publishResult, "publication result"))
	waitCoreTunnelTest(t, committed, "publication commit")
	waitCoreTunnelTest(t, finished, "listener completion")
	require.ErrorIs(t, handle.Wait(), listenerErr)
}

func TestTunnelListenerRegistryClosesLateHandleAndPreservesFirstCause(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	first := newFakeTunnelListenerHandle()
	require.NoError(t, registry.AddOrClose(first))
	firstCause := errors.New("first listener failed")
	handles, gotCause := registry.BeginClose(firstCause)
	require.ErrorIs(t, gotCause, firstCause)
	require.Equal(t, []tunnelListenerHandle{first}, handles)

	lateCloseErr := errors.New("late close failed")
	late := newFakeTunnelListenerHandle()
	late.closeErr = lateCloseErr
	err := registry.AddOrClose(late)
	require.ErrorIs(t, err, firstCause)
	require.Equal(t, 1, late.closes())
	gotCause, cleanupErr := registry.Result()
	require.ErrorIs(t, gotCause, firstCause)
	require.ErrorIs(t, cleanupErr, lateCloseErr)
}

func countExactError(err, target error) int {
	if err == nil {
		return 0
	}
	count := 0
	if err == target {
		count++
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			count += countExactError(child, target)
		}
	case interface{ Unwrap() error }:
		count += countExactError(wrapped.Unwrap(), target)
	}
	return count
}

func TestTunnelStartupKeepsFirstListenerCauseOverLaterListenFailure(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	closeErr := errors.New("first listener cleanup failed")
	first := newFakeTunnelListenerHandle()
	first.closeErr = closeErr
	require.NoError(t, registry.AddOrClose(first))

	shutdownCtx, stop := context.WithCancelCause(t.Context())
	var detaches atomic.Int32
	shutdown := newTunnelShutdown(registry, stop, func() { detaches.Add(1) })
	firstCause := errors.New("first listener failed")
	require.ErrorIs(t, shutdown(firstCause), closeErr)
	require.Equal(t, firstCause, context.Cause(shutdownCtx))

	laterListenErr := errors.New("second listen canceled")
	cleanupErr := shutdown(laterListenErr)
	storedCause, _ := registry.Result()
	startupErr := errors.Join(storedCause, cleanupErr)
	require.ErrorIs(t, startupErr, firstCause)
	require.ErrorIs(t, startupErr, closeErr)
	require.NotErrorIs(t, startupErr, laterListenErr)
	require.Equal(t, 1, countExactError(startupErr, firstCause))
	require.Equal(t, int32(1), detaches.Load())
	require.Equal(t, 1, first.closes())
}

func TestTunnelListenerRegistryPreservesCreationOrder(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	first := newFakeTunnelListenerHandle()
	second := newFakeTunnelListenerHandle()
	require.NoError(t, registry.AddOrClose(first))
	require.NoError(t, registry.AddOrClose(second))

	handles, _ := registry.BeginClose(errors.New("stop"))
	require.Equal(t, []tunnelListenerHandle{first, second}, handles)
}

func TestTunnelShutdownOwnsDetachAndCleanupOnce(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	closeOrder := make(chan string, 2)
	firstCloseErr := errors.New("first close failed")
	first := newFakeTunnelListenerHandle()
	first.closeErr = firstCloseErr
	first.onClose = func() { closeOrder <- "first" }
	second := newFakeTunnelListenerHandle()
	second.onClose = func() { closeOrder <- "second" }
	require.NoError(t, registry.AddOrClose(first))
	require.NoError(t, registry.AddOrClose(second))

	shutdownCtx, stop := context.WithCancelCause(t.Context())
	var detaches atomic.Int32
	shutdown := newTunnelShutdown(registry, stop, func() { detaches.Add(1) })
	firstCause := errors.New("listener failed")
	require.ErrorIs(t, shutdown(firstCause), firstCloseErr)
	require.Equal(t, firstCause, context.Cause(shutdownCtx))
	require.Equal(t, "first", waitCoreTunnelTest(t, closeOrder, "first listener close"))
	require.Equal(t, "second", waitCoreTunnelTest(t, closeOrder, "second listener close"))
	require.Equal(t, int32(1), detaches.Load())
	require.Equal(t, 1, first.closes())
	require.Equal(t, 1, second.closes())

	require.ErrorIs(t, shutdown(errors.New("later stop")), firstCloseErr)
	require.Equal(t, int32(1), detaches.Load())
	require.Equal(t, 1, first.closes())
	require.Equal(t, 1, second.closes())
}

func TestTunnelUpstreamMonitorStopsAfterSharedBindingDetach(t *testing.T) {
	t.Parallel()
	services := NewServices()
	key := ServiceKey{
		Digest:    digest.FromString("shared-tunnel-upstream"),
		SessionID: "test-session",
		Kind:      ServiceRuntimeShared,
	}
	waitStarted := make(chan struct{})
	waitExited := make(chan struct{})
	upstream := &RunningService{
		Key:  key,
		Host: "shared-upstream",
		Wait: func(ctx context.Context) error {
			close(waitStarted)
			<-ctx.Done()
			close(waitExited)
			return context.Cause(ctx)
		},
	}
	services.running[key] = upstream
	services.bindings[key] = 2

	registry := &tunnelListenerRegistry{}
	monitorCtx, stop := context.WithCancelCause(t.Context())
	shutdown := newTunnelShutdown(registry, stop, func() { services.Detach(monitorCtx, upstream) })
	monitorDone := make(chan struct{})
	go func() {
		monitorTunnelUpstream(monitorCtx, upstream, shutdown)
		close(monitorDone)
	}()
	waitCoreTunnelTest(t, waitStarted, "shared upstream monitor start")

	require.NoError(t, shutdown(errors.New("tunnel stopped")))
	waitCoreTunnelTest(t, waitExited, "shared upstream monitor exit")
	waitCoreTunnelTest(t, monitorDone, "shared upstream monitor completion")
	services.l.Lock()
	defer services.l.Unlock()
	require.Same(t, upstream, services.running[key])
	require.Equal(t, 1, services.bindings[key])
}

func TestTunnelShutdownExplicitStopCanWinFinishedListener(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	handle := newFakeTunnelListenerHandle()
	require.NoError(t, registry.AddOrClose(handle))
	listenerErr := errors.New("listener already finished")
	handle.finish(listenerErr)

	shutdownCtx, stop := context.WithCancelCause(t.Context())
	var detaches atomic.Int32
	shutdown := newTunnelShutdown(registry, stop, func() { detaches.Add(1) })
	stopErr := errors.New("explicit stop")
	require.NoError(t, shutdown(stopErr))
	require.Equal(t, stopErr, context.Cause(shutdownCtx))
	require.ErrorIs(t, handle.Wait(), listenerErr)
	require.Equal(t, int32(1), detaches.Load())
}

func TestTunnelShutdownListenerCompletionCanWinExplicitStop(t *testing.T) {
	t.Parallel()
	registry := &tunnelListenerRegistry{}
	handle := newFakeTunnelListenerHandle()
	require.NoError(t, registry.AddOrClose(handle))
	listenerErr := errors.New("listener failed")
	handle.finish(listenerErr)

	shutdownCtx, stop := context.WithCancelCause(t.Context())
	var detaches atomic.Int32
	shutdown := newTunnelShutdown(registry, stop, func() { detaches.Add(1) })
	require.NoError(t, shutdown(listenerErr))
	require.NoError(t, shutdown(errors.New("later explicit stop")))
	require.Equal(t, listenerErr, context.Cause(shutdownCtx))
	require.Equal(t, int32(1), detaches.Load())
}

func TestTunnelStartupRollbackModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"ordinary", "detachable"} {
		t.Run(mode, func(t *testing.T) {
			modeCtx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
				SessionID:         mode,
				DetachableSession: mode == "detachable",
			})
			t.Run("first listen fails", func(t *testing.T) {
				registry := &tunnelListenerRegistry{}
				shutdownCtx, stop := context.WithCancelCause(modeCtx)
				var detaches atomic.Int32
				shutdown := newTunnelShutdown(registry, stop, func() { detaches.Add(1) })
				setupErr := errors.New("first listen setup failed")

				require.NoError(t, shutdown(setupErr))
				require.Equal(t, setupErr, context.Cause(shutdownCtx))
				require.Equal(t, int32(1), detaches.Load())
				handles, _ := registry.BeginClose(errors.New("later"))
				require.Empty(t, handles)
			})

			t.Run("later port fails", func(t *testing.T) {
				registry := &tunnelListenerRegistry{}
				first := newFakeTunnelListenerHandle()
				require.NoError(t, registry.AddOrClose(first))
				shutdownCtx, stop := context.WithCancelCause(modeCtx)
				var detaches atomic.Int32
				shutdown := newTunnelShutdown(registry, stop, func() { detaches.Add(1) })
				setupErr := errors.New("later port setup failed")

				require.NoError(t, shutdown(setupErr))
				require.Equal(t, setupErr, context.Cause(shutdownCtx))
				require.Equal(t, int32(1), detaches.Load())
				require.Equal(t, 1, first.closes())
			})
		})
	}
}

type tunnelAssemblyServer struct {
	*mockServer
	services *Services
}

func (server *tunnelAssemblyServer) Services(context.Context) (*Services, error) {
	return server.services, nil
}

type tunnelAssemblyFixture struct {
	ctx                context.Context
	services           *Services
	tunnel             *Service
	tunnelKey          ServiceKey
	upstream           *RunningService
	upstreamKey        ServiceKey
	upstreamWaitExited chan struct{}
}

func newTunnelAssemblyFixture(
	t *testing.T,
	mode string,
	portCount int,
	listen tunnelListenHostToContainer,
) *tunnelAssemblyFixture {
	t.Helper()
	services := NewServices()
	server := &tunnelAssemblyServer{mockServer: &mockServer{}, services: services}
	query := &Query{Server: server}
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		SessionID:         "assembled-tunnel-" + mode,
		ClientID:          "assembled-client-" + mode,
		DetachableSession: mode == "detachable",
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = ContextWithQuery(ctx, query)
	dag := newCoreDagqlServerForTest(t, query)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Service]{}))
	upstreamService := &Service{CustomHostname: "shared-upstream"}
	upstreamResult, err := dagql.NewObjectResultForCall(upstreamService, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "assembled_tunnel_upstream_" + mode,
		Type:        dagql.NewResultCallType(upstreamService.Type()),
	})
	require.NoError(t, err)
	upstreamDigest, err := upstreamResult.ContentPreferredDigest(ctx)
	require.NoError(t, err)
	upstreamKey := ServiceKey{
		Digest:    upstreamDigest,
		SessionID: "assembled-tunnel-" + mode,
		Kind:      ServiceRuntimeShared,
	}
	upstreamWaitExited := make(chan struct{})
	upstream := &RunningService{
		Key:  upstreamKey,
		Host: "shared-upstream",
		Wait: func(ctx context.Context) error {
			<-ctx.Done()
			close(upstreamWaitExited)
			return context.Cause(ctx)
		},
	}
	services.running[upstreamKey] = upstream
	services.bindings[upstreamKey] = 1

	ports := make([]PortForward, portCount)
	for i := range ports {
		ports[i] = PortForward{Backend: 8000 + i, Protocol: NetworkProtocolTCP}
	}
	tunnelDigest := digest.FromString("assembled-tunnel-" + mode)
	return &tunnelAssemblyFixture{
		ctx:      ctx,
		services: services,
		tunnel: &Service{
			TunnelUpstream:            upstreamResult,
			TunnelPorts:               ports,
			testListenHostToContainer: listen,
		},
		tunnelKey: ServiceKey{
			Digest:    tunnelDigest,
			SessionID: "assembled-tunnel-" + mode,
			Kind:      ServiceRuntimeShared,
		},
		upstream:           upstream,
		upstreamKey:        upstreamKey,
		upstreamWaitExited: upstreamWaitExited,
	}
}

func requireTunnelAssemblyClean(t *testing.T, fixture *tunnelAssemblyFixture) {
	t.Helper()
	waitCoreTunnelTest(t, fixture.upstreamWaitExited, "assembled upstream monitor exit")
	fixture.services.l.Lock()
	defer fixture.services.l.Unlock()
	require.NotContains(t, fixture.services.starting, fixture.tunnelKey)
	require.NotContains(t, fixture.services.running, fixture.tunnelKey)
	require.NotContains(t, fixture.services.bindings, fixture.tunnelKey)
	require.Same(t, fixture.upstream, fixture.services.running[fixture.upstreamKey])
	require.Equal(t, 1, fixture.services.bindings[fixture.upstreamKey])
}

type tunnelAssemblyRef struct {
	released chan struct{}
	once     sync.Once
	count    atomic.Int32
}

func (*tunnelAssemblyRef) ID() string         { return "assembled-tunnel-ref" }
func (*tunnelAssemblyRef) SnapshotID() string { return "assembled-tunnel-snapshot" }
func (*tunnelAssemblyRef) Size(context.Context) (int64, error) {
	return 0, nil
}
func (*tunnelAssemblyRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return nil, nil
}
func (ref *tunnelAssemblyRef) Release(context.Context) error {
	ref.count.Add(1)
	ref.once.Do(func() { close(ref.released) })
	return nil
}

func TestStartTunnelAssembledFailurePoints(t *testing.T) {
	for _, mode := range []string{"ordinary", "detachable"} {
		t.Run(mode, func(t *testing.T) {
			t.Run("first listener setup failure", func(t *testing.T) {
				listenErr := errors.New("first listener setup failed")
				var listens atomic.Int32
				fixture := newTunnelAssemblyFixture(t, mode, 1, func(
					context.Context, string, string, string,
				) (*h2c.ListenResponse, tunnelListenerHandle, error) {
					listens.Add(1)
					return nil, nil, listenErr
				})

				_, err := fixture.services.Start(fixture.ctx, fixture.tunnelKey.Digest, fixture.tunnel, false)
				require.ErrorIs(t, err, listenErr)
				require.Equal(t, 1, countExactError(err, listenErr))
				require.Equal(t, int32(1), listens.Load())
				requireTunnelAssemblyClean(t, fixture)
			})

			t.Run("later listener setup failure", func(t *testing.T) {
				first := newFakeTunnelListenerHandle()
				first.waitReturned = make(chan struct{})
				listenErr := errors.New("later listener setup failed")
				var listens atomic.Int32
				fixture := newTunnelAssemblyFixture(t, mode, 2, func(
					context.Context, string, string, string,
				) (*h2c.ListenResponse, tunnelListenerHandle, error) {
					if listens.Add(1) == 1 {
						return &h2c.ListenResponse{Addr: "127.0.0.1:31001"}, first, nil
					}
					return nil, nil, listenErr
				})

				_, err := fixture.services.Start(fixture.ctx, fixture.tunnelKey.Digest, fixture.tunnel, false)
				require.ErrorIs(t, err, listenErr)
				require.Equal(t, 1, countExactError(err, listenErr))
				require.Equal(t, int32(2), listens.Load())
				require.Equal(t, 1, first.closes())
				waitCoreTunnelTest(t, first.waitReturned, "first assembled listener monitor exit")
				requireTunnelAssemblyClean(t, fixture)
			})

			t.Run("completion before publication", func(t *testing.T) {
				listenerErr := errors.New("listener failed before publication")
				listener := newFakeTunnelListenerHandle()
				listener.waitReturned = make(chan struct{})
				waitRelease := make(chan struct{})
				listener.waitRelease = waitRelease
				listener.finish(listenerErr)
				fixture := newTunnelAssemblyFixture(t, mode, 1, func(
					context.Context, string, string, string,
				) (*h2c.ListenResponse, tunnelListenerHandle, error) {
					return &h2c.ListenResponse{Addr: "127.0.0.1:31002"}, listener, nil
				})

				_, err := fixture.services.Start(fixture.ctx, fixture.tunnelKey.Digest, fixture.tunnel, false)
				require.ErrorIs(t, err, listenerErr)
				require.Equal(t, 1, countExactError(err, listenerErr))
				require.Equal(t, 1, listener.closes())
				close(waitRelease)
				waitCoreTunnelTest(t, listener.waitReturned, "pre-publication listener monitor exit")
				requireTunnelAssemblyClean(t, fixture)
			})

			t.Run("completion after publication", func(t *testing.T) {
				listener := newFakeTunnelListenerHandle()
				listener.guardEntered = make(chan struct{})
				guardRelease := make(chan struct{})
				listener.guardRelease = guardRelease
				listener.waitReturned = make(chan struct{})
				fixture := newTunnelAssemblyFixture(t, mode, 1, func(
					context.Context, string, string, string,
				) (*h2c.ListenResponse, tunnelListenerHandle, error) {
					return &h2c.ListenResponse{Addr: "127.0.0.1:31003"}, listener, nil
				})
				type startResult struct {
					running *RunningService
					err     error
				}
				started := make(chan startResult, 1)
				go func() {
					running, err := fixture.services.Start(fixture.ctx, fixture.tunnelKey.Digest, fixture.tunnel, false)
					started <- startResult{running: running, err: err}
				}()
				waitCoreTunnelTest(t, listener.guardEntered, "assembled publication guard")
				close(guardRelease)
				result := waitCoreTunnelTest(t, started, "assembled tunnel publication")
				require.NoError(t, result.err)
				require.NotNil(t, result.running)

				ref := &tunnelAssemblyRef{released: make(chan struct{})}
				result.running.refsMu.Lock()
				result.running.refs = append(result.running.refs, ref)
				result.running.refsMu.Unlock()
				listenerErr := errors.New("listener failed after publication")
				listener.finish(listenerErr)

				waitCoreTunnelTest(t, listener.waitReturned, "published listener monitor exit")
				waitCoreTunnelTest(t, ref.released, "published tunnel tracked resource release")
				require.ErrorIs(t, result.running.Wait(t.Context()), listenerErr)
				require.Equal(t, int32(1), ref.count.Load())
				require.Equal(t, 1, listener.closes())
				requireTunnelAssemblyClean(t, fixture)
			})
		})
	}
}

var _ tunnelListenerHandle = (*fakeTunnelListenerHandle)(nil)
var _ bkcache.Ref = (*tunnelAssemblyRef)(nil)
