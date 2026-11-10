package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/session/filesync"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseHostPolicy(t *testing.T) {
	t.Run("empty means no policy", func(t *testing.T) {
		for _, value := range []string{"", "  \n"} {
			policy, err := ParseHostPolicy(value)
			require.NoError(t, err)
			require.Nil(t, policy)
		}
	})

	t.Run("valid policy", func(t *testing.T) {
		policy, err := ParseHostPolicy(`{
			"version": 1,
			"reason": "CI",
			"secrets": {"providers": ["op", "aws+sm"]},
			"files": {"read": ["/work/"]},
			"git": {"credentials": true},
			"registryAuth": true
		}`)
		require.NoError(t, err)
		require.True(t, policy.AllowsRead("/work/dagger.toml"))
		require.False(t, policy.AllowsRead("/workspace"))
		require.False(t, policy.AllowsRead("/work/../etc/passwd"))
	})

	// A client must not run with a policy that it does not fully understand.
	for name, value := range map[string]string{
		"missing version":    `{}`,
		"unknown version":    `{"version": 2}`,
		"unknown field":      `{"version": 1, "network": {"tcp": true}}`,
		"unknown provider":   `{"version": 1, "secrets": {"providers": ["op", "keychain"]}}`,
		"relative read path": `{"version": 1, "files": {"read": ["work"]}}`,
		"trailing data":      `{"version": 1} {"version": 1}`,
		"malformed":          `{"version": 1`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := ParseHostPolicy(value)
			require.Error(t, err)
		})
	}
}

func TestHostPolicyReads(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.txt"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), nil, 0o600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "file-link")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "dir-link")))

	newEnforcer := func(t *testing.T) *hostPolicyEnforcer {
		e, err := newHostPolicyEnforcer(&HostPolicy{Version: 1, Files: HostPolicyFiles{Read: []string{root}}})
		require.NoError(t, err)
		return e
	}

	for _, p := range []string{
		root,
		filepath.Join(root, "inside.txt"),
		filepath.Join(root, "missing", "file"),
	} {
		t.Run("allows "+p, func(t *testing.T) {
			e := newEnforcer(t)
			err := e.checkRead(p)
			require.NoError(t, err)
			require.NoError(t, e.err())
		})
	}

	for _, p := range []string{
		filepath.Join(outside, "secret.txt"),
		filepath.Join(root, "..", filepath.Base(outside), "secret.txt"),
		filepath.Join(root, "file-link"),
		filepath.Join(root, "dir-link", "secret.txt"),
		filepath.Join(root, "dir-link", "missing"),
	} {
		t.Run("refuses "+p, func(t *testing.T) {
			e := newEnforcer(t)
			err := e.checkRead(p)
			require.Equal(t, codes.PermissionDenied, status.Code(err))
			require.ErrorContains(t, e.err(), "host access denied by session policy")
			require.Error(t, e.stopped.Err(), "the first refusal stops the session")
		})
	}

	t.Run("stats outside the read roots find nothing", func(t *testing.T) {
		e := newEnforcer(t)
		require.True(t, e.canStat(filepath.Join(root, "inside.txt")))
		require.True(t, e.canStat(filepath.Dir(root)), "a sync of the root stats its parents")
		require.False(t, e.canStat(filepath.Join(filepath.Dir(root), "sibling")))
		require.False(t, e.canStat(filepath.Join(outside, "secret.txt")))
		require.False(t, e.canStat(filepath.Join(root, "dir-link", "secret.txt")))
		require.NoError(t, e.err(), "a stat is not a refusal")
	})

	t.Run("find-up stops at the read root", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(root), "marker"), nil, 0o600))
		e := newEnforcer(t)
		res, err := policyFilesyncSource{policy: e}.FindUp(t.Context(), &filesync.FindUpRequest{Start: root, Names: []string{"marker"}})
		require.NoError(t, err)
		require.Empty(t, res.Found)
	})
}
