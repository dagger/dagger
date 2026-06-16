package core

import (
	"testing"

	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

func TestExecNetModeDefault(t *testing.T) {
	t.Parallel()

	mode, err := execNetMode(ContainerExecOpts{})
	require.NoError(t, err)
	require.Equal(t, pb.NetMode_UNSET, mode)
}

func TestExecNetModeNone(t *testing.T) {
	t.Parallel()

	mode, err := execNetMode(ContainerExecOpts{NoNetwork: true})
	require.NoError(t, err)
	require.Equal(t, pb.NetMode_NONE, mode)
}

func TestExecNetModeHost(t *testing.T) {
	t.Parallel()

	mode, err := execNetMode(ContainerExecOpts{HostNetwork: true})
	require.NoError(t, err)
	require.Equal(t, pb.NetMode_HOST, mode)
}

func TestExecNetModeConflict(t *testing.T) {
	t.Parallel()

	_, err := execNetMode(ContainerExecOpts{
		NoNetwork:   true,
		HostNetwork: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot set both noNetwork and hostNetwork")
}
