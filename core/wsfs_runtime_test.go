package core

import (
	"context"
	"testing"

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

		cleanup, err := ctr.setupWSFSMounts(ctx, ContainerMountData{})
		require.NoError(t, err)
		require.NotNil(t, cleanup)
		require.NoError(t, cleanup())
	})

	t.Run("workspace mounts present", func(t *testing.T) {
		ctr := &Container{Mounts: ContainerMounts{
			{Target: "/src", WorkspaceSource: &WorkspaceMountSource{}},
			{Target: "/data", WorkspaceSource: &WorkspaceMountSource{}},
		}}

		cleanup, err := ctr.setupWSFSMounts(ctx, ContainerMountData{})
		require.Nil(t, cleanup)
		require.Error(t, err)
		require.Contains(t, err.Error(), "wsfs runtime not implemented yet")
		require.Contains(t, err.Error(), "/src")
		require.Contains(t, err.Error(), "/data")
	})
}
