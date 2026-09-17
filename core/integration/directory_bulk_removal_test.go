package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (DirectorySuite) TestWithoutDirectories(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	root := t.TempDir()
	var paths []string
	for i := range 400 {
		name := fmt.Sprintf("remove-%03d", i)
		require.NoError(t, os.MkdirAll(filepath.Join(root, name, "nested"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name, "nested", "file"), []byte("remove me"), 0o644))
		paths = append(paths, name)
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep"), []byte("keep me"), 0o644))
	result := c.Host().Directory(root).WithoutDirectories(paths)
	entries, err := result.Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"keep"}, entries)
	contents, err := result.File("keep").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "keep me", contents)
}
