package engineutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostBindMountReadonlyInvariant(t *testing.T) {
	// The default hostBindMount is used by injectInit to mount the dagger-init
	// binary read-only. Writable requests must be rejected unless the caller
	// explicitly opts in via allowRW (setupHostMounts does, for user host
	// mounts and volume mounts). These subtests pin the rule in both directions.

	t.Run("default rejects writable request", func(t *testing.T) {
		m := hostBindMount{srcPath: "/some/path"}
		_, err := m.Mount(context.Background(), false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "host bind mounts must be readonly")
	})

	t.Run("default accepts readonly request and sets ro+rbind", func(t *testing.T) {
		m := hostBindMount{srcPath: "/some/path"}
		ref, err := m.Mount(context.Background(), true)
		require.NoError(t, err)
		mounts, _, err := ref.Mount()
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		require.Equal(t, "bind", mounts[0].Type)
		require.Equal(t, "/some/path", mounts[0].Source)
		require.Contains(t, mounts[0].Options, "rbind")
		require.Contains(t, mounts[0].Options, "ro")
	})

	t.Run("allowRW accepts writable request and omits ro", func(t *testing.T) {
		m := hostBindMount{srcPath: "/some/path", allowRW: true}
		ref, err := m.Mount(context.Background(), false)
		require.NoError(t, err)
		mounts, _, err := ref.Mount()
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		require.Contains(t, mounts[0].Options, "rbind")
		require.NotContains(t, mounts[0].Options, "ro")
	})

	t.Run("allowRW still honors readonly request", func(t *testing.T) {
		m := hostBindMount{srcPath: "/some/path", allowRW: true}
		ref, err := m.Mount(context.Background(), true)
		require.NoError(t, err)
		mounts, _, err := ref.Mount()
		require.NoError(t, err)
		require.Contains(t, mounts[0].Options, "ro")
	})
}
