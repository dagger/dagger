//go:build !darwin && !windows

package engineutil

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/stretchr/testify/require"
)

// Telemetry, nested-client and new-session listeners are created before runc
// starts the container. Host must not look up its PID; none must stay isolated.
func TestRunInHostNetworkNamespace(t *testing.T) {
	GetGlobalNamespaceWorkerPool().Start()
	t.Cleanup(func() {
		ShutdownGlobalNamespaceWorkerPool()
		// This test owns the pool. Repeated runs need a fresh singleton after
		// shutdown, rather than restarting the closed production pool.
		globalNSWorkerPool = nil
		globalNSWorkerPoolOnce = sync.Once{}
	})

	t.Run("host listener before container start", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		ns, err := network.NewHostProvider().New(ctx, "")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, ns.Close()) })
		state := &execState{id: "not-started", done: make(chan struct{}), networkNamespace: ns}
		listener, err := runInNetNS(ctx, state, func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		})
		require.NoError(t, err)
		require.NoError(t, listener.Close())
	})

	t.Run("none provider does not inherit host", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		ns, err := network.NewNoneProvider().New(ctx, "")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, ns.Close()) })
		state := &execState{id: "not-started", done: make(chan struct{}), networkNamespace: ns}
		called := false
		_, err = runInNetNS(ctx, state, func() (bool, error) {
			called = true
			return true, nil
		})
		require.Error(t, err)
		require.False(t, called)
	})

	t.Run("host respects cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("cancelled before listener setup")
		cancel(cause)
		ns, err := network.NewHostProvider().New(ctx, "")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, ns.Close()) })
		state := &execState{id: "not-started", done: make(chan struct{}), networkNamespace: ns}
		called := false
		_, err = runInNetNS(ctx, state, func() (bool, error) {
			called = true
			return true, nil
		})
		require.ErrorIs(t, err, cause)
		require.False(t, called)
	})

	t.Run("host preserves callback errors", func(t *testing.T) {
		ns, err := network.NewHostProvider().New(t.Context(), "")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, ns.Close()) })
		state := &execState{id: "not-started", done: make(chan struct{}), networkNamespace: ns}
		cause := errors.New("listener unavailable")
		_, err = runInNetNS(t.Context(), state, func() (net.Listener, error) {
			return nil, cause
		})
		require.ErrorIs(t, err, cause)
	})

	t.Run("host cancellation interrupts waiting for callback", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		ns, err := network.NewHostProvider().New(ctx, "")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, ns.Close()) })
		state := &execState{id: "not-started", done: make(chan struct{}), networkNamespace: ns}
		started := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		result := make(chan error, 1)
		go func() {
			_, err := runInNetNS(ctx, state, func() (bool, error) {
				close(started)
				<-release
				return true, nil
			})
			result <- err
		}()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("listener callback did not start")
		}
		cause := errors.New("cancelled during listener setup")
		cancel(cause)
		select {
		case err := <-result:
			require.ErrorIs(t, err, cause)
		case <-time.After(3 * time.Second):
			t.Fatal("cancellation waited for the listener callback")
		}
	})
}
