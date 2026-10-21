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

// Listeners for an exec are opened before runc starts its container, so
// runInNetNS must work without the container's PID.
func TestRunInHostNetworkNamespace(t *testing.T) {
	GetGlobalNamespaceWorkerPool().Start()
	t.Cleanup(func() {
		ShutdownGlobalNamespaceWorkerPool()
		// Let repeated runs (-count=2) start a fresh pool.
		globalNSWorkerPool = nil
		globalNSWorkerPoolOnce = sync.Once{}
	})

	notStarted := func(t *testing.T, provider network.Provider) *execState {
		ns, err := provider.New(t.Context(), "")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, ns.Close()) })
		return &execState{id: "not-started", done: make(chan struct{}), networkNamespace: ns}
	}

	t.Run("host opens a listener before the container starts", func(t *testing.T) {
		state := notStarted(t, network.NewHostProvider())
		listener, err := runInNetNS(t.Context(), state, func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		})
		require.NoError(t, err)
		require.NoError(t, listener.Close())
	})

	t.Run("none does not run in the host network", func(t *testing.T) {
		state := notStarted(t, network.NewNoneProvider())
		called := false
		_, err := runInNetNS(t.Context(), state, func() (bool, error) {
			called = true
			return true, nil
		})
		require.Error(t, err)
		require.False(t, called)
	})

	t.Run("host cancellation does not wait for the callback", func(t *testing.T) {
		state := notStarted(t, network.NewHostProvider())
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		started := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		// Let the callback finish before the test ends, so its worker doesn't
		// outlive the test.
		defer func() { close(release); <-finished }()
		result := make(chan error, 1)
		go func() {
			_, err := runInNetNS(ctx, state, func() (bool, error) {
				defer close(finished)
				close(started)
				<-release
				return true, nil
			})
			result <- err
		}()
		<-started
		cause := errors.New("cancelled during listener setup")
		cancel(cause)
		select {
		case err := <-result:
			require.ErrorIs(t, err, cause)
		case <-time.After(3 * time.Second):
			t.Fatal("cancellation waited for the callback")
		}
	})
}
