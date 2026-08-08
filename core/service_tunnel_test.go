package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
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
	handle.completionMu.Lock()
	defer handle.completionMu.Unlock()
	return handle.result
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
	gotCause, handles := registry.BeginClose(firstCause)
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

	_, handles := registry.BeginClose(errors.New("stop"))
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
				_, handles := registry.BeginClose(errors.New("later"))
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

var _ tunnelListenerHandle = (*fakeTunnelListenerHandle)(nil)
