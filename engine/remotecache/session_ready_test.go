package remotecache

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// The roots the adapter finds ready, of those exports left out busy, go to
// the service as Ready, each batch as it comes.
func TestReadyTellsTheServiceOfBusyRootsThatCanBeCaptured(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	s, conn, _ := servedSession(t, adapter)
	conn.none(t)

	adapter.ready <- []uint64{7, 9}
	ready := conn.next(t)
	require.Equal(t, protocol.TypeReady, ready.Type)
	require.Equal(t, protocol.Ready{Roots: []uint64{7, 9}}, decode[protocol.Ready](t, ready))
	s.dispatch(reply(ready.ID, protocol.TypeReadied, protocol.Readied{}))

	adapter.ready <- []uint64{11}
	ready = conn.next(t)
	require.Equal(t, protocol.Ready{Roots: []uint64{11}}, decode[protocol.Ready](t, ready))
	s.dispatch(reply(ready.ID, protocol.TypeReadied, protocol.Readied{}))
	conn.none(t)
}

// A connection that ends while a Ready waits for its reply still comes back
// to the adapter, whose look for ready roots then ends with the connection:
// that end is where the adapter logs what its looks cost.
func TestReadyComesBackToTheAdapterAsTheConnectionEnds(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	s, conn, _ := servedSession(t, adapter)
	adapter.ready <- []uint64{7}
	ready := conn.next(t)
	require.Equal(t, protocol.TypeReady, ready.Type)

	s.cancel(errors.New("the connection ended"))
	select {
	case <-adapter.readyEnded:
	case <-time.After(10 * time.Second):
		t.Fatal("the session ended without its look for ready roots ending")
	}
}
