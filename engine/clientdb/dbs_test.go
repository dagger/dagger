package clientdb

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDBRefCount(t *testing.T) {
	root := t.TempDir()

	dbs := NewDBs(root)

	ctx := t.Context()

	c1 := "client1"
	d1a, err := dbs.Open(ctx, c1)
	require.NoError(t, err)
	require.Len(t, dbs.open, 1)
	require.Equal(t, d1a.refCount, 1)

	d1b, err := dbs.Open(ctx, c1)
	require.NoError(t, err)
	require.Len(t, dbs.open, 1)
	require.Equal(t, d1a.refCount, 2)

	_, err = d1a.Read().SelectSpansSince(ctx, SelectSpansSinceParams{
		ID:    1,
		Limit: 1,
	})
	require.NoError(t, err)

	require.NoError(t, d1a.Close())
	require.Len(t, dbs.open, 1)
	require.NotNil(t, d1a.writer)
	require.NotNil(t, d1a.reader)
	require.NotNil(t, d1a.writeQueries)
	require.NotNil(t, d1a.readQueries)
	require.NotNil(t, d1a.writeAgent)
	require.Equal(t, d1a.refCount, 1)

	_, err = d1b.Read().SelectSpansSince(ctx, SelectSpansSinceParams{
		ID:    1,
		Limit: 1,
	})
	require.NoError(t, err)

	c2 := "client2"
	d2a, err := dbs.Open(ctx, c2)
	require.NoError(t, err)
	require.Len(t, dbs.open, 2)
	require.Equal(t, d2a.refCount, 1)

	require.NoError(t, d1b.Close())
	require.Len(t, dbs.open, 1)
	require.Nil(t, d1a.writer)
	require.Nil(t, d1b.writer)
	require.Nil(t, d1a.reader)
	require.Nil(t, d1b.reader)
	require.Nil(t, d1a.writeQueries)
	require.Nil(t, d1b.writeQueries)
	require.Nil(t, d1a.readQueries)
	require.Nil(t, d1b.readQueries)
	require.Equal(t, d1a.refCount, 0)
	require.Equal(t, d1b.refCount, 0)
	select {
	case <-d1a.writeAgent.done:
	default:
		t.Fatal("client DB writer goroutine still running after final close")
	}

	_, err = d2a.Read().SelectSpansSince(ctx, SelectSpansSinceParams{
		ID:    1,
		Limit: 1,
	})
	require.NoError(t, err)

	require.NoError(t, d2a.Close())
	require.Len(t, dbs.open, 0)
	require.Nil(t, d2a.writer)
	require.Nil(t, d2a.reader)
	require.Nil(t, d2a.writeQueries)
	require.Nil(t, d2a.readQueries)
	require.Equal(t, d2a.refCount, 0)
	select {
	case <-d2a.writeAgent.done:
	default:
		t.Fatal("client DB writer goroutine still running after final close")
	}
}

func TestDBCloseNil(t *testing.T) {
	var db *DB
	require.NoError(t, db.Close())
}

func TestOpenWithNonDirRoot(t *testing.T) {
	root := t.TempDir()
	blocker := root + "/not-a-dir"
	require.NoError(t, os.WriteFile(blocker, []byte("nope"), 0600))

	dbs := NewDBs(blocker)
	_, err := dbs.Open(t.Context(), "client1")
	require.Error(t, err)
}
