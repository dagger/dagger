package remotecache

import (
	"testing"

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
