package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
)

// fakeMutableRef / fakeImmutableRef are non-nil stand-ins for refs whose
// methods the code under test must not call.
type fakeMutableRef struct{ bkcache.MutableRef }

type fakeImmutableRef struct{ bkcache.ImmutableRef }

func TestPathWithin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		p, dir string
		want   bool
	}{
		{"/src", "/", true},
		{"/", "/", true},
		{"/src", "/src", true},
		{"/src/a/b", "/src", true},
		{"/srcx", "/src", false}, // prefix of the name, not a parent directory
		{"/src", "/src/a", false},
		{"/other", "/src", false},
	} {
		require.Equal(t, tc.want, pathWithin(tc.p, tc.dir), "pathWithin(%q, %q)", tc.p, tc.dir)
	}
}

func TestSelectWorkdirMounts(t *testing.T) {
	t.Parallel()
	root := &execMountState{Dest: pb.RootMount}
	src := &execMountState{Dest: "/src"}
	srcx := &execMountState{Dest: "/srcx"}
	cache := &execMountState{Dest: "/src/node_modules/.cache"}
	nodeModules := &execMountState{Dest: "/src/node_modules"}
	sub := &execMountState{Dest: "/src/sub"}
	other := &execMountState{Dest: "/data"}
	states := []*execMountState{root, cache, src, srcx, nodeModules, sub, other}

	t.Run("workdir on the rootfs", func(t *testing.T) {
		covering, nested := selectWorkdirMounts(states, "/work")
		require.Same(t, root, covering)
		require.Empty(t, nested)
	})

	t.Run("workdir is a mount; nested mounts shallowest first", func(t *testing.T) {
		covering, nested := selectWorkdirMounts(states, "/src")
		require.Same(t, src, covering)
		require.Equal(t, []*execMountState{nodeModules, sub, cache}, nested)
	})

	t.Run("workdir inside a mount picks the innermost", func(t *testing.T) {
		covering, nested := selectWorkdirMounts(states, "/src/sub/deeper")
		require.Same(t, sub, covering)
		require.Empty(t, nested)
	})

	t.Run("sibling with a shared name prefix is not nested", func(t *testing.T) {
		covering, nested := selectWorkdirMounts(states, "/src")
		require.Same(t, src, covering)
		require.NotContains(t, nested, srcx)
		require.NotContains(t, nested, other)
	})

	t.Run("workdir / nests every other mount", func(t *testing.T) {
		covering, nested := selectWorkdirMounts(states, "/")
		require.Same(t, root, covering)
		require.Len(t, nested, len(states)-1)
		require.Equal(t, "/src", mountDest(nested[0])) // depth 1 before depth 2/3
		require.Equal(t, "/src/node_modules/.cache", mountDest(nested[len(nested)-1]))
	})

	t.Run("no mount covers the workdir", func(t *testing.T) {
		covering, _ := selectWorkdirMounts([]*execMountState{src}, "/elsewhere")
		require.Nil(t, covering)
	})
}

func TestCaptureLiveMountUnsupported(t *testing.T) {
	t.Parallel()
	active := fakeMutableRef{}
	base := fakeImmutableRef{}
	for name, state := range map[string]*execMountState{
		"no active ref":     {Dest: "/src", SourceRef: base},
		"read-only":         {Dest: "/src", Readonly: true, SourceRef: base, ActiveRef: active},
		"cache volume":      {Dest: "/src", Volume: &execVolumeMountConfig{}, ActiveRef: active},
		"tmpfs":             {Dest: "/src", TmpfsOpt: &pb.TmpfsOpt{}, ActiveRef: active},
		"secret":            {Dest: "/run/s", Secret: &execSecretMountConfig{}, ActiveRef: active},
		"ssh":               {Dest: "/run/ssh", SSH: &execSSHMountConfig{}, ActiveRef: active},
		"subpath selector":  {Dest: "/src", Selector: "/sub", SourceRef: base, ActiveRef: active},
		"mutable source":    {Dest: "/src", SourceRef: fakeMutableRef{}, ActiveRef: active},
		"relative selector": {Dest: "/src", Selector: "sub/dir", SourceRef: base, ActiveRef: active},
	} {
		t.Run(name, func(t *testing.T) {
			// Unsupported mounts are rejected before touching the query or
			// any ref, so a nil query is fine here.
			_, err := captureLiveMount(t.Context(), nil, state, t.TempDir())
			require.ErrorIs(t, err, errLiveMountUnsupported)
		})
	}
}

func TestSensitiveMountPaths(t *testing.T) {
	t.Parallel()
	rootfs := t.TempDir()
	states := []*execMountState{
		{Dest: pb.RootMount},
		{Dest: "/src"},
		{Dest: "/run/secrets/token", Secret: &execSecretMountConfig{}},
		{Dest: "/run/ssh.sock", SSH: &execSSHMountConfig{}},
		{Dest: "/cache", Volume: &execVolumeMountConfig{}},
	}
	skip, err := sensitiveMountPaths(rootfs, states)
	require.NoError(t, err)
	require.Len(t, skip, 2)
	require.Contains(t, skip, filepath.Join(rootfs, "run/secrets/token"))
	require.Contains(t, skip, filepath.Join(rootfs, "run/ssh.sock"))
}

func TestSnapshotInteractiveTerminalScoping(t *testing.T) {
	t.Parallel()
	unregister := registerInteractiveTerminal("term-scoping-test", &interactiveTerminal{
		sessionID: "owner-session",
		workdir:   "/",
	})
	defer unregister()

	ctxFor := func(sessionID string) context.Context {
		return engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
			ClientID:  "client",
			SessionID: sessionID,
		})
	}

	t.Run("unknown terminal", func(t *testing.T) {
		_, err := SnapshotInteractiveTerminal(ctxFor("owner-session"), "no-such-terminal")
		require.ErrorContains(t, err, "is not running")
	})

	t.Run("another session cannot snapshot the terminal", func(t *testing.T) {
		_, err := SnapshotInteractiveTerminal(ctxFor("other-session"), "term-scoping-test")
		// Indistinguishable from an unknown terminal: don't confirm the ID exists.
		require.ErrorContains(t, err, "is not running")
	})

	t.Run("unregistered terminals are gone", func(t *testing.T) {
		unregisterTmp := registerInteractiveTerminal("term-scoping-tmp", &interactiveTerminal{sessionID: "owner-session"})
		unregisterTmp()
		_, err := SnapshotInteractiveTerminal(ctxFor("owner-session"), "term-scoping-tmp")
		require.ErrorContains(t, err, "is not running")
	})
}

func TestIsPseudoFS(t *testing.T) {
	t.Parallel()
	require.True(t, isPseudoFS("/proc"))
	require.False(t, isPseudoFS(t.TempDir()))
}

func TestCopyLiveTree(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "dir/sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(src, "dir/sub/exec.sh"), []byte("#!/bin/sh"), 0o755))
	require.NoError(t, os.Symlink("../file.txt", filepath.Join(src, "dir/link")))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "run/secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "run/secrets/token"), []byte("s3cr3t"), 0o600))

	t.Run("copies files, dirs, symlinks and modes; skips sensitive paths", func(t *testing.T) {
		dest := t.TempDir()
		skip := map[string]struct{}{filepath.Join(src, "run/secrets/token"): {}}
		require.NoError(t, copyLiveTree(t.Context(), src, dest, skip))

		b, err := os.ReadFile(filepath.Join(dest, "file.txt"))
		require.NoError(t, err)
		require.Equal(t, "hello", string(b))

		fi, err := os.Stat(filepath.Join(dest, "file.txt"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())
		fi, err = os.Stat(filepath.Join(dest, "dir/sub/exec.sh"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
		fi, err = os.Stat(filepath.Join(dest, "dir/sub"))
		require.NoError(t, err)
		require.True(t, fi.IsDir())
		require.Equal(t, os.FileMode(0o750), fi.Mode().Perm())

		target, err := os.Readlink(filepath.Join(dest, "dir/link"))
		require.NoError(t, err)
		require.Equal(t, "../file.txt", target, "symlinks must be copied as symlinks")

		_, err = os.Stat(filepath.Join(dest, "run/secrets/token"))
		require.ErrorIs(t, err, os.ErrNotExist, "sensitive mounts must never be copied")
	})

	t.Run("size cap", func(t *testing.T) {
		old := interactiveTerminalSnapshotMaxBytes
		interactiveTerminalSnapshotMaxBytes = 4 // smaller than file.txt
		defer func() { interactiveTerminalSnapshotMaxBytes = old }()

		err := copyLiveTree(t.Context(), src, t.TempDir(), nil)
		require.ErrorContains(t, err, "too large to snapshot")
	})

	t.Run("canceled context stops the copy", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := copyLiveTree(ctx, src, t.TempDir(), nil)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("missing source fails", func(t *testing.T) {
		err := copyLiveTree(t.Context(), filepath.Join(src, "nope"), t.TempDir(), nil)
		require.Error(t, err)
		require.False(t, strings.Contains(err.Error(), "too large"))
	})
}
