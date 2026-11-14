package dagql

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// A read in pieces that ends between two of its pieces holds egraphMu there,
// not at all: its deferred done lets go of nothing, and the lock is free
// after. A test's failed check in testReadPieceYielded ends the read there.
func TestReadInPiecesEndedBetweenPiecesLetsGoOfNothing(t *testing.T) {
	t.Parallel()
	c := &Cache{readPieceSize: 1}
	c.testReadPieceYielded = runtime.Goexit
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		hold := c.readInPieces()
		defer hold.done()
		hold.step()
	}()
	<-ended
	require.True(t, c.egraphMu.TryLock(), "the read holds nothing")
	c.egraphMu.Unlock()
}
