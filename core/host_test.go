package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostFindUpAllPrefersExistsFS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	appDir := filepath.Join(repoDir, "app")
	startDir := filepath.Join(appDir, "sub")

	fs := &existsAwareStatFS{
		exists: map[string]bool{
			filepath.Join(repoDir, ".git"):   true,
			filepath.Join(appDir, ".dagger"): true,
		},
	}

	found, err := Host{}.FindUpAll(context.Background(), fs, startDir, map[string]struct{}{
		".git":    {},
		".dagger": {},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		".git":    repoDir,
		".dagger": appDir,
	}, found)
	require.Zero(t, fs.statCalls)
	require.Greater(t, fs.existsCalls, 0)
}

func TestHostFindUpAllFallsBackToStatFS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	startDir := filepath.Join(repoDir, "app")

	fs := &statOnlyFS{
		exists: map[string]bool{
			filepath.Join(repoDir, ".git"): true,
		},
	}

	found, err := Host{}.FindUpAll(context.Background(), fs, startDir, map[string]struct{}{
		".git": {},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		".git": repoDir,
	}, found)
	require.Greater(t, fs.statCalls, 0)
}

type existsAwareStatFS struct {
	exists      map[string]bool
	existsCalls int
	statCalls   int
}

func (fs *existsAwareStatFS) Exists(_ context.Context, path string) (string, bool, error) {
	fs.existsCalls++
	if fs.exists[path] {
		return filepath.Dir(path), true, nil
	}
	return "", false, nil
}

func (fs *existsAwareStatFS) Stat(_ context.Context, _ string) (string, *Stat, error) {
	fs.statCalls++
	return "", nil, errors.New("unexpected Stat call")
}

type statOnlyFS struct {
	exists    map[string]bool
	statCalls int
}

func (fs *statOnlyFS) Stat(_ context.Context, path string) (string, *Stat, error) {
	fs.statCalls++
	if fs.exists[path] {
		return filepath.Dir(path), &Stat{Name: filepath.Base(path)}, nil
	}
	return "", nil, os.ErrNotExist
}
