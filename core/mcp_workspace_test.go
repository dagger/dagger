package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceMountSummary(t *testing.T) {
	prev := &Workspace{mountPoints: []string{"mnt/kept", "mnt/removed"}}
	next := &Workspace{mountPoints: []string{"config", "mnt/added", "mnt/kept"}}
	require.Equal(t, "Mounted (read-only): config\nMounted (read-only): mnt/added\nUnmounted: mnt/removed", summarizeMountChanges(prev, next))
	require.Equal(t, []string{"config", "mnt/added", "mnt/kept", "mnt/removed"}, unionMountPoints(prev, next))
	require.Empty(t, summarizeMountChanges(prev, prev))
	require.Empty(t, summarizeMountChanges(nil, nil))

	// Combining and sorting mount paths must not mutate either workspace,
	// including when the source slice has capacity to spare.
	points := make([]string, 2, 8)
	copy(points, prev.mountPoints)
	prev.mountPoints = points
	unionMountPoints(prev, next)
	require.Equal(t, []string{"mnt/kept", "mnt/removed"}, prev.MountPoints())
	require.Equal(t, []string{"config", "mnt/added", "mnt/kept"}, next.MountPoints())
	cloned := prev.MountPoints()
	cloned[0] = "changed"
	require.Equal(t, "mnt/kept", prev.MountPoints()[0])
}

// The notices rebindWorkspace/adoptLLM hand the model when a swap cannot be
// summarized as a patch: same-origin moves and outright replacements.
func TestWorkspaceSwapNotices(t *testing.T) {
	f := newWorkspaceRelationFixture(t)

	t.Run("unrelated workspaces are replaced, not diffed", func(t *testing.T) {
		prev := localWorkspace("/src/api", "c1", "github.com/acme/api")
		next := gitRefWorkspace(f.gitRef("https://github.com/other/thing", "refs/heads/main", shaB))
		require.Equal(t,
			"Workspace replaced: github.com/acme/api [file:///src/api] -> github.com/other/thing @"+shaB+" (main) [git-ref://"+shaB+"]. "+
				"Files were not diffed; the previous workspace is no longer reachable through your tools.",
			summarizeWorkspaceReplaced(prev, next))
	})

	t.Run("move with known distance", func(t *testing.T) {
		from := DescribeWorkspace(gitRefWorkspace(f.gitRef("https://github.com/acme/api", "refs/heads/main", shaA)))
		to := DescribeWorkspace(gitRefWorkspace(f.gitRef("https://github.com/acme/api", "refs/tags/v1.0.0", shaB)))
		out := renderWorkspaceMove(from, to, workspaceMoveDistance{known: true, ahead: 3, behind: 120})
		require.Equal(t,
			"Workspace moved: github.com/acme/api @"+shaA+" (main) -> @"+shaB+" (v1.0.0). "+
				"The new checkout is 3 commit(s) ahead of and 100+ behind the previous one. "+
				"Files were not diffed; the previous checkout is no longer reachable through your tools.",
			out)
	})

	t.Run("move without distance or head falls back to what is known", func(t *testing.T) {
		from := DescribeWorkspace(localWorkspace("/src/api", "c1", "github.com/acme/api"))
		to := DescribeWorkspace(gitRefWorkspace(f.gitRef("https://github.com/acme/api", "refs/heads/main", shaA)))
		out := renderWorkspaceMove(from, to, workspaceMoveDistance{})
		require.Equal(t,
			"Workspace moved: github.com/acme/api file:///src/api -> @"+shaA+" (main). "+
				"Files were not diffed; the previous checkout is no longer reachable through your tools.",
			out)
	})

	t.Run("move takes its location from whichever side knows the origin", func(t *testing.T) {
		from := WorkspaceIdentity{Origin: "github.com/acme/api", SHA: shaA}
		to := WorkspaceIdentity{Address: "file:///tmp/api", SHA: shaB}
		require.Contains(t, renderWorkspaceMove(from, to, workspaceMoveDistance{}),
			"Workspace moved: github.com/acme/api @"+shaA+" -> @"+shaB+".")
	})
}
