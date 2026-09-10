package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceResetArgsValidation(t *testing.T) {
	full := strings.Repeat("a1", 20)
	require.NoError(t, workspaceResetArgs{Commit: full}.validate())
	require.NoError(t, workspaceResetArgs{Commit: strings.Repeat("a1", 32)}.validate())
	for _, invalid := range []string{"", "HEAD", "main", full[:12], strings.ToUpper(full), full + "aa"} {
		require.ErrorContains(t, workspaceResetArgs{Commit: invalid}.validate(), "full lowercase commit hash")
	}
}
