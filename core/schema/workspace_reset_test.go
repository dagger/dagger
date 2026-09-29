package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceResetArgsValidation(t *testing.T) {
	full := strings.Repeat("a1", 20)
	for _, valid := range []string{
		full,
		strings.Repeat("a1", 32),
		full[:7],
		full[:4],
		"HEAD",
		"main",
		"refs/heads/main",
		"HEAD~",
		"HEAD~3",
		"HEAD^2",
		"main~1^2",
		full[:7] + "~2",
	} {
		require.NoError(t, workspaceWithResetArgs{Commit: valid}.validate(), valid)
	}
	require.ErrorContains(t, workspaceWithResetArgs{}.validate(), "must not be empty")
	for _, invalid := range []string{"HEAD^{tree}", "HEAD@{1}", "main..HEAD", "HEAD:path", "@", "~1", "^main"} {
		require.ErrorContains(t, workspaceWithResetArgs{Commit: invalid}.validate(), "invalid revision", invalid)
	}
}
