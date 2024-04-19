package filesync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
)

func TestFileSyncIncludePatterns(t *testing.T) {
	ctx := context.TODO()
	t.Parallel()

	tmpDir := t.TempDir()
	tmpFS, err := fsutil.NewFS(tmpDir)
	require.NoError(t, err)
	destDir := t.TempDir()

	err = os.WriteFile(filepath.Join(tmpDir, "foo"), []byte("content1"), 0600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "bar"), []byte("content2"), 0600)
	require.NoError(t, err)

	s, err := session.NewSession(ctx, "foo", "bar")
	require.NoError(t, err)

	m, err := session.NewManager()
	require.NoError(t, err)

	fs := NewFSSyncProvider(StaticDirSource{"test0": tmpFS})
	s.Allow(fs)

	dialer := session.Dialer(testutil.TestStream(testutil.Handler(m.HandleConn)))

	g, ctx := errgroup.WithContext(context.Background())

	g.Go(func() error {
		return s.Run(ctx, dialer)
	})

	g.Go(func() (reterr error) {
		c, err := m.Get(ctx, s.ID(), false)
		if err != nil {
			return err
		}
		if err := FSSync(ctx, c, FSSendRequestOpt{
			Name:            "test0",
			DestDir:         destDir,
			IncludePatterns: []string{"ba*"},
		}); err != nil {
			return err
		}

		_, err = os.ReadFile(filepath.Join(destDir, "foo"))
		assert.Error(t, err)

		dt, err := os.ReadFile(filepath.Join(destDir, "bar"))
		if err != nil {
			return err
		}
		assert.Equal(t, "content2", string(dt))
		return s.Close()
	})

	err = g.Wait()
	require.NoError(t, err)
}
