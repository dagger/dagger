package core

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestContainerSelectionReusesSyntheticParents(t *testing.T) {
	source := &containerSelectionSource{}
	t.Cleanup(source.Close)
	first, err := source.stub("/missing")
	require.NoError(t, err)
	for range 1000 {
		backing, err := source.stub("/missing/.")
		require.NoError(t, err)
		require.Same(t, first, backing)
		f, err := backing.root.OpenFile(".", unix.O_PATH, 0)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	require.Len(t, source.closers, 1, "repeated lookups must not retain more handles")
	source.Close()
	_, err = first.root.OpenFile(".", unix.O_PATH, 0)
	require.ErrorIs(t, err, os.ErrClosed)
}
