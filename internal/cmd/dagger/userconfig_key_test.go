package daggercmd

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUserConfigWorkspaceKeyRemoteRefs pins the user-config key derived for
// -W remote workspace refs: the normalized clone address, with any version
// or subdir selection dropped so one key spans every ref of the repo.
func TestUserConfigWorkspaceKeyRemoteRefs(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	for ref, want := range map[string]string{
		"https://github.com/dagger/dagger":            "github.com/dagger/dagger",
		"https://github.com/dagger/dagger.git":        "github.com/dagger/dagger",
		"github.com/dagger/dagger":                    "github.com/dagger/dagger",
		"github.com/dagger/dagger@main":               "github.com/dagger/dagger",
		"git@github.com:dagger/dagger.git":            "github.com/dagger/dagger",
		"https://github.com/dagger/dagger.git#main":   "github.com/dagger/dagger",
		"https://github.com/dagger/dagger#main:docs":  "github.com/dagger/dagger",
		"github.com/dagger/dagger/modules/wolfi@main": "github.com/dagger/dagger",
	} {
		workspaceRef = ref
		key, err := userConfigWorkspaceKey(context.Background())
		require.NoError(t, err, "ref %q", ref)
		require.Equal(t, want, key, "ref %q", ref)
	}
}

// TestUserConfigWorkspaceKeyLocalCheckout covers the fallback for local
// selections: the checkout's git origin.
func TestUserConfigWorkspaceKeyLocalCheckout(t *testing.T) {
	oldWorkspaceRef := workspaceRef
	t.Cleanup(func() {
		workspaceRef = oldWorkspaceRef
	})

	repoDir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:acme/api.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	workspaceRef = repoDir
	key, err := userConfigWorkspaceKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/api", key)

	// A local checkout without a usable remote has no key.
	bare := t.TempDir()
	cmd := exec.Command("git", "init", "-q", filepath.Base(bare))
	cmd.Dir = filepath.Dir(bare)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	workspaceRef = bare
	_, err = userConfigWorkspaceKey(context.Background())
	require.Error(t, err)
}
