package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestContainerHasWorkspaceMount(t *testing.T) {
	t.Run("no workspace mounts", func(t *testing.T) {
		ctr := &Container{Mounts: ContainerMounts{{Target: "/src"}}}
		require.False(t, ctr.HasWorkspaceMount())
	})

	t.Run("workspace mount present", func(t *testing.T) {
		ctr := &Container{Mounts: ContainerMounts{{
			Target:          "/src",
			WorkspaceSource: &WorkspaceMountSource{},
		}}}
		require.True(t, ctr.HasWorkspaceMount())
	})
}

func TestSetupWSFSMounts(t *testing.T) {
	ctx := context.Background()

	t.Run("no workspace mounts", func(t *testing.T) {
		ctr := &Container{Mounts: ContainerMounts{{Target: "/src"}}}

		cleanup, err := ctr.setupWSFSMounts(ctx, ContainerMountData{}, nil)
		require.NoError(t, err)
		require.NotNil(t, cleanup)
		require.NoError(t, cleanup())
	})

	t.Run("workspace mounts present", func(t *testing.T) {
		ctr := &Container{Mounts: ContainerMounts{
			{Target: "/src", WorkspaceSource: &WorkspaceMountSource{}},
			{Target: "/data", WorkspaceSource: &WorkspaceMountSource{}},
		}}

		cleanup, err := ctr.setupWSFSMounts(ctx, ContainerMountData{}, nil)
		require.NoError(t, err)
		require.NotNil(t, cleanup)
		require.NoError(t, cleanup())
	})
}

func TestResolveWorkspaceMountDirUsesUpper(t *testing.T) {
	ctx := context.Background()

	upper := dagql.ObjectResult[*Directory]{}
	workspaceMnt := &WorkspaceMountSource{
		Upper: &upper,
	}

	resolved, err := resolveWorkspaceMountDir(ctx, workspaceMnt)
	require.NoError(t, err)
	require.Same(t, workspaceMnt.Upper, resolved)
}
