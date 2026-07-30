package secretprovider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCmdProviderResolvesRelativeToWorkspaceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.json"), []byte(`{}`), 0o644))

	scriptPath := filepath.Join(root, "scripts", "print-secret.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf hunter2"), 0o755))

	// Invoke from a subdirectory of the workspace, with a cmd:// value
	// written relative to the workspace root - this is exactly the case
	// that fails without cmd.Dir set.
	subdir := filepath.Join(root, "some", "module", "dir")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	ctx := withWorkspaceRoot(t.Context(), root)
	out, err := cmdProvider(ctx, "scripts/print-secret.sh")
	require.NoError(t, err)
	require.Equal(t, "hunter2", string(out))
}

func TestCmdProviderFallsBackToProcessCWDWithoutWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "print-secret.sh"), []byte("#!/bin/sh\nprintf hunter2"), 0o755))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(wd)) }()

	// No workspace root in context (as if no dagger.json was found) - must
	// still behave like before this change: relative to the process CWD.
	out, err := cmdProvider(context.Background(), "./print-secret.sh")
	require.NoError(t, err)
	require.Equal(t, "hunter2", string(out))
}

func TestFindWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.json"), []byte(`{}`), 0o644))

	subdir := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(subdir))
	defer func() { require.NoError(t, os.Chdir(wd)) }()

	got := findWorkspaceRoot()
	// Resolve symlinks on both sides (e.g. /tmp -> /private/tmp on macOS).
	wantResolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	require.Equal(t, wantResolved, gotResolved)
}

func TestFindWorkspaceRootNoneFound(t *testing.T) {
	dir := t.TempDir()

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer func() { require.NoError(t, os.Chdir(wd)) }()

	// t.TempDir() is under the OS temp dir, which shouldn't itself contain
	// a dagger.json anywhere above it.
	require.Empty(t, findWorkspaceRoot())
}
