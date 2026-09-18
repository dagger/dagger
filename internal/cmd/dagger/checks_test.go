package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScaleOutFlagScopedToCheck(t *testing.T) {
	flag := checksCmd.Flags().Lookup("scale-out")
	require.NotNil(t, flag)
	require.True(t, flag.Hidden)
	require.Nil(t, rootCmd.PersistentFlags().Lookup("scale-out"))
	version, _, err := rootCmd.Find([]string{"version"})
	require.NoError(t, err)
	require.Nil(t, version.Flags().Lookup("scale-out"))
}

func TestValidateCheckSelection(t *testing.T) {
	t.Run("unfiltered empty selection is allowed", func(t *testing.T) {
		require.NoError(t, validateCheckSelection(nil, 0))
	})

	t.Run("non-empty selection is allowed", func(t *testing.T) {
		require.NoError(t, validateCheckSelection([]string{"lint"}, 1))
	})

	t.Run("single unmatched pattern fails", func(t *testing.T) {
		require.EqualError(t,
			validateCheckSelection([]string{"missing"}, 0),
			`no checks matched pattern "missing"`,
		)
	})

	t.Run("multiple unmatched patterns fail", func(t *testing.T) {
		require.EqualError(t,
			validateCheckSelection([]string{"missing-one", "missing-two"}, 0),
			`no checks matched any of the patterns: "missing-one", "missing-two"`,
		)
	})
}
