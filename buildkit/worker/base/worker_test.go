package base

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestID(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	id0, err := ID(tmpdir)
	require.NoError(t, err)

	id1, err := ID(tmpdir)
	require.NoError(t, err)

	require.Equal(t, id0, id1)

	// reset tmpdir
	require.NoError(t, os.RemoveAll(tmpdir))
	require.NoError(t, os.MkdirAll(tmpdir, 0700))

	id2, err := ID(tmpdir)
	require.NoError(t, err)

	require.NotEqual(t, id0, id2)
}
