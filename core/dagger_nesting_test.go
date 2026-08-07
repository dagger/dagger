package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

func TestDaggerNestingMode(t *testing.T) {
	mode, err := daggerNestingMode(false, dagql.Optional[DaggerNesting]{})
	require.NoError(t, err)
	require.Equal(t, engineutil.DaggerNestingNone, mode)

	mode, err = daggerNestingMode(true, dagql.Optional[DaggerNesting]{})
	require.NoError(t, err)
	require.Equal(t, engineutil.DaggerNestingNone, mode, "legacy nesting keeps the marker absent")

	mode, err = daggerNestingMode(false, dagql.Opt(DaggerNestingNestedClient))
	require.NoError(t, err)
	require.Equal(t, engineutil.DaggerNestingNestedClient, mode)

	mode, err = daggerNestingMode(false, dagql.Opt(DaggerNestingIndependentSessions))
	require.NoError(t, err)
	require.Equal(t, engineutil.DaggerNestingIndependentSessions, mode)

	_, err = daggerNestingMode(true, dagql.Opt(DaggerNestingNestedClient))
	require.ErrorContains(t, err, "cannot be combined")
	_, err = daggerNestingMode(true, dagql.Opt(DaggerNestingIndependentSessions))
	require.ErrorContains(t, err, "cannot be combined")
}
