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
	t.Run("no policy", func(t *testing.T) {
		for name, value := range map[string]string{
			"empty file":     "",
			"other settings": "[engine]\nleave_old_engines = true\n",
			// The rest of the file is read leniently.
			"malformed other settings": "[engine\nleave_old_engines = true\n",
		} {
			t.Run(name, func(t *testing.T) {
				policy, err := ParseHostPolicy([]byte(value))
				require.NoError(t, err)
				require.Nil(t, policy)
			})
		}
	})

	t.Run("valid policy", func(t *testing.T) {
		policy, err := ParseHostPolicy([]byte(`
[engine]
leave_old_engines = true

[host_policy]
version = 1
reason = "CI"
registry_auth = true

[host_policy.secrets]
providers = ["op", "aws+sm"]

[host_policy.files]
read = ["/work/"]

[host_policy.git]
credentials = true
`))
		require.NoError(t, err)
		require.Equal(t, &HostPolicy{
			Version:      1,
			Reason:       "CI",
			RegistryAuth: true,
			Secrets:      HostPolicySecrets{Providers: []string{"op", "aws+sm"}},
			Files:        HostPolicyFiles{Read: []string{"/work"}},
			Git:          HostPolicyGit{Credentials: true},
		}, policy)
	})

	// A client must not run with a policy that it does not fully understand.
	for name, value := range map[string]string{
		"missing version":       "[host_policy]\n",
		"unknown version":       "[host_policy]\nversion = 2\n",
		"unknown field":         "[host_policy]\nversion = 1\n[host_policy.network]\ntcp = true\n",
		"unknown nested field":  "[host_policy]\nversion = 1\n[host_policy.files]\nwrite = [\"/work\"]\n",
		"wrong type":            "[host_policy]\nversion = 1\n[host_policy.secrets]\nproviders = \"op\"\n",
		"unknown provider":      "[host_policy]\nversion = 1\n[host_policy.secrets]\nproviders = [\"op\", \"keychain\"]\n",
		"relative read path":    "[host_policy]\nversion = 1\n[host_policy.files]\nread = [\"work\"]\n",
		"not a table":           "host_policy = 1\n",
		"array of tables":       "[[host_policy]]\nversion = 1\n",
		"malformed with policy": "[host_policy]\nversion = 1\n[engine\n",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := ParseHostPolicy([]byte(value))
			require.Error(t, err)
		})
	}
}

func TestLoadHostPolicy(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		policy, err := loadHostPolicy(filepath.Join(t.TempDir(), "config.toml"))
		require.NoError(t, err)
		require.Nil(t, policy)
	})

	t.Run("unreadable file", func(t *testing.T) {
		// A directory cannot be read as a file. The CLI cannot know whether
		// it has a policy.
		_, err := loadHostPolicy(t.TempDir())
		require.Error(t, err)
	})
}

func TestHostPolicyAllowsRead(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(outside, ".env"), filepath.Join(root, ".env")))
	policy := &HostPolicy{Version: 1, Files: HostPolicyFiles{Read: []string{root}}}

	require.True(t, policy.AllowsRead(filepath.Join(root, "dagger.toml")))
	require.False(t, policy.AllowsRead(filepath.Join(root, ".env")), "a symlink out of the read path")
	require.False(t, policy.AllowsRead(filepath.Join(root, "..", filepath.Base(outside), "x")))
	require.False(t, policy.AllowsRead(""))
	require.False(t, policy.AllowsRead("dagger.toml"), "a relative path")
}

func TestHostPolicyReads(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.txt"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), nil, 0o600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "file-link")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "dir-link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling-link")))
	require.NoError(t, os.Symlink("loop", filepath.Join(root, "loop")))

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
		filepath.Join(root, "dangling-link"),
		filepath.Join(root, "dangling-link", "file"),
		filepath.Join(root, "loop"),
	} {
		t.Run("refuses "+p, func(t *testing.T) {
			e := newEnforcer(t)
			err := e.checkRead(p)
			require.Equal(t, codes.PermissionDenied, status.Code(err))
			require.ErrorContains(t, e.err(), "host access denied by session policy")
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

	t.Run("find-up names stay under the read root", func(t *testing.T) {
		e := newEnforcer(t)
		res, err := policyFilesyncSource{policy: e}.FindUp(t.Context(), &filesync.FindUpRequest{
			Start: root,
			Names: []string{
				"inside.txt",
				filepath.Join("..", filepath.Base(outside), "secret.txt"),
				filepath.Join("dir-link", "secret.txt"),
			},
		})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"inside.txt": filepath.ToSlash(root)}, res.Found)
		require.NoError(t, e.err(), "a find-up is not a refusal")
	})
}
