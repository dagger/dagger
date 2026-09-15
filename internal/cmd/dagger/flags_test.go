package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVolumeCustomFlagValue(t *testing.T) {
	val := GetCustomFlagValue(Volume)
	require.IsType(t, &volumeValue{}, val)
	require.Equal(t, Volume, val.Type())

	require.ErrorContains(t, val.Set(""), "volume address cannot be empty")
	require.NoError(t, val.Set("sshfs://git@example.com/repo?privateKey=env://KEY&knownHosts=env://KNOWN_HOSTS"))
	require.Equal(t, "sshfs://git@example.com/repo?privateKey=env://KEY&knownHosts=env://KNOWN_HOSTS", val.String())

	require.NoError(t, val.Set("engine-volume://datasets/models?subdir=llama%2Fweights"))
	require.Equal(t, "engine-volume://datasets/models?subdir=llama%2Fweights", val.String())
}

func TestVolumeSliceCustomFlagValue(t *testing.T) {
	val, err := GetCustomFlagValueSlice(Volume, []string{
		"sshfs://git@example.com/one?privateKey=env://KEY&knownHosts=env://KNOWN_HOSTS",
		"engine-volume://datasets/two?subdir=weights",
	})
	require.NoError(t, err)
	require.Equal(t, "[]"+Volume, val.Type())
	require.Contains(t, val.String(), "sshfs://git@example.com/one")
	require.Contains(t, val.String(), "engine-volume://datasets/two")
}
