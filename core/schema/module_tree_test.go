package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleTreePath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		moduleDir, address, want string
	}{
		{"modules/app", "../lib", "modules/lib"},
		{"modules/app", "./sub", "modules/app/sub"},
		{"modules/app", ".", "modules/app"},
		{"modules/app", "/modules/lib", "modules/lib"},
		{"modules/app", "/", "."},
		{"modules/app", "../..", "."},
		{".", "lib", "lib"},
		{".", "/lib/../other", "other"},
	} {
		got, err := moduleTreePath(tc.moduleDir, tc.address)
		require.NoError(t, err, tc.address)
		require.Equal(t, tc.want, got, tc.address)
	}

	for _, tc := range []struct {
		moduleDir, address string
	}{
		{"modules/app", "../../.."},
		{"modules/app", "../../../outside"},
		{".", ".."},
	} {
		_, err := moduleTreePath(tc.moduleDir, tc.address)
		require.ErrorContains(t, err, "leaves the module's own tree", tc.address)
	}
}
