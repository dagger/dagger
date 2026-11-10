package daggercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func TestGitLabCloudRowsPreserveRepositoryAndMergeRequest(t *testing.T) {
	const repo = "gitlab.com/Team/Subgroup/PrivateProject"
	now := time.Now()
	sha := strings.Repeat("a", 64)
	rows := cloudCheckRows("example", []cloudapi.CheckCommit{{
		Repo: "https://" + repo, CommitSHA: sha, Timestamp: now,
		Refs:   []cloudapi.CheckCommitRef{{Typename: "CheckCommitPullRequestRef", Number: 12, URL: "https://" + repo + "/-/merge_requests/12"}},
		Checks: []cloudapi.Check{{ID: "check", Name: "lint", Status: "success", ModuleRef: repo, ModuleVersion: sha}},
	}})
	selected, err := cloudRowsForWorkspaceAddress(t.Context(), rows, repo+".git@merge-requests/12/head", []string{"lint"})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	kind, address := cloudCheckWorkspaceAddress(selected[0])
	require.Equal(t, "mr", kind)
	require.Equal(t, repo+"@merge-requests/12/head", address)
	for _, other := range []string{"gitlab.com/team/Subgroup/PrivateProject", "another.gitlab.test/Team/Subgroup/PrivateProject"} {
		selected, err := cloudRowsForWorkspaceAddress(t.Context(), rows, other+".git@"+sha, nil)
		require.NoError(t, err)
		require.Empty(t, selected, fmt.Sprintf("%s must not match %s", other, repo))
	}
}

func TestLocalGitLabChecksKeepTheKnownRepositoryRoot(t *testing.T) {
	oldWorkspace := workspaceRef
	workspaceRef = ""
	t.Cleanup(func() { workspaceRef = oldWorkspace })
	checkout := t.TempDir()
	runGit(t, checkout, "init")
	runGit(t, checkout, "remote", "add", "origin", "http://127.0.0.1:9/Team/Subgroup/PrivateProject.git")
	runGit(t, checkout, "checkout", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "dagger.toml"), []byte("# workspace\n"), 0o600))
	t.Chdir(checkout)
	remote, _, err := selectedRemoteWorkspaceAddress(t.Context(), "cloud checks")
	require.NoError(t, err, "the local origin already identifies the root; HTTP discovery is unnecessary")
	require.Equal(t, "http://127.0.0.1:9/Team/Subgroup/PrivateProject", remote.CloneRef)
	require.Equal(t, "feature", remote.Version)
	require.Equal(t, ".", remote.Path)
}
