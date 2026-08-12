package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("supports address fragment ref", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, ".", ref.WorkspaceSubdir)
	})

	t.Run("supports address fragment ref and subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main:toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "toolchains/changelog", ref.WorkspaceSubdir)
	})

	t.Run("supports legacy at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "github.com/dagger/dagger/toolchains/changelog@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "toolchains/changelog", ref.WorkspaceSubdir)
	})

	t.Run("preserves legacy https at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, ".", ref.WorkspaceSubdir)
	})
}

func TestNormalizeWorkspaceRemoteSubdir(t *testing.T) {
	t.Parallel()

	t.Run("empty becomes dot", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeWorkspaceRemoteSubdir("")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("absolute gets normalized to relative", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeWorkspaceRemoteSubdir("/toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "toolchains/changelog", got)
	})

	t.Run("rejects escaping paths", func(t *testing.T) {
		t.Parallel()
		_, err := NormalizeWorkspaceRemoteSubdir("../outside")
		require.ErrorContains(t, err, "outside repository")
	})
}
