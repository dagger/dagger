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

// An enum flag takes a member's name in any case, and the name the legacy
// naming rules would give it, so the spellings users typed before a module
// moved to the new rules keep working; the CLI sends the canonical name.
func TestEnumFlagValue(t *testing.T) {
	typedef := &modEnum{Name: "Arch", Members: []*modEnumMember{
		{Name: "ARM64"}, {Name: "GITHUB"}, {Name: "E2E_TEST"}, {Name: "FOO_BAR"}, {Name: "FOOBAR"},
	}}
	val := newEnumValue(typedef, "")
	for given, want := range map[string]string{
		"ARM64":      "ARM64",
		"arm64":      "ARM64",
		"ARM_64":     "ARM64",
		"GIT_HUB":    "GITHUB",
		"E_2_E_TEST": "E2E_TEST",
		"e2e-test":   "E2E_TEST",
		"foo_bar":    "FOO_BAR",
		"FOOBAR":     "FOOBAR",
	} {
		require.NoError(t, val.Set(given), given)
		require.Equal(t, want, val.String(), given)
	}
	// Ambiguous once separators are ignored.
	require.ErrorContains(t, val.Set("FOO_B_AR"), "value should be one of")
	require.ErrorContains(t, val.Set("ARM65"), "value should be one of")
}
