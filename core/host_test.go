package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/engineutil"
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

// A filesystem that can search its own parents, like a client's host, is asked
// once for every name, instead of once per name and directory level: each of
// those is a round trip to the client when the engine is remote.
func TestHostFindUpAllAsksFindUpFSOnce(t *testing.T) {
	t.Parallel()

	fs := &findUpFS{found: map[string]string{".env": "/home/user"}}
	found, err := Host{}.FindUpAll(context.Background(), fs, "/home/user/a/b/c/d", map[string]struct{}{
		".env": {},
		".git": {},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{".env": "/home/user"}, found)
	require.Equal(t, 1, fs.findUpCalls)
	require.ElementsMatch(t, []string{".env", ".git"}, fs.names)
	require.Equal(t, "/home/user/a/b/c/d", fs.start)
	require.Zero(t, fs.statCalls)
}

// A client too old to search its own parents gets the per-level search.
func TestHostFindUpAllFallsBackWhenFindUpUnsupported(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	startDir := filepath.Join(repoDir, "app")

	fs := &findUpFS{
		unsupported: true,
		statOnlyFS:  statOnlyFS{exists: map[string]bool{filepath.Join(repoDir, ".git"): true}},
	}
	found, err := Host{}.FindUpAll(context.Background(), fs, startDir, map[string]struct{}{
		".git": {},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{".git": repoDir}, found)
	require.Equal(t, 1, fs.findUpCalls)
	require.Greater(t, fs.statCalls, 0)
}

type findUpFS struct {
	statOnlyFS
	found       map[string]string
	unsupported bool
	findUpCalls int
	start       string
	names       []string
}

func (fs *findUpFS) FindUpAll(_ context.Context, start string, names []string) (map[string]string, error) {
	fs.findUpCalls++
	fs.start, fs.names = start, names
	if fs.unsupported {
		return nil, engineutil.ErrFindUpUnsupported
	}
	return fs.found, nil
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
