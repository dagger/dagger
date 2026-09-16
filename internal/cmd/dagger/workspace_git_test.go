package daggercmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceGitCommands(t *testing.T) {
	for _, workspace := range []string{"workspace", "ws"} {
		for _, name := range []string{"ref", "sha", "dirty", "log", "url"} {
			t.Run(workspace+" "+name, func(t *testing.T) {
				cmd, args, err := rootCmd.Find([]string{workspace, "git", name})
				require.NoError(t, err)
				require.Empty(t, args)
				require.Equal(t, name, cmd.Name())
				require.Same(t, workspaceGitCmd, cmd.Parent())
				require.NoError(t, cmd.ValidateArgs(nil))
				require.Error(t, cmd.ValidateArgs([]string{"unexpected"}))
				require.True(t, commandHasCapability(cmd, mayCallEngine))
				require.True(t, commandHasCapability(cmd, maySelectWorkspace))
				require.False(t, commandHasCapability(cmd, mayReadWorkspaceConfig))
				require.NoError(t, validateFlagCapabilities(testRootCommand(), []string{workspace, "git", name, "-W", "github.com/dagger/dagger", "--engine=auto"}))
			})
		}
	}
}

func TestWorkspaceGitLogLimit(t *testing.T) {
	for _, limit := range []string{"-1", "0", "1", "25"} {
		t.Run(limit, func(t *testing.T) {
			cmd := newWorkspaceGitLogCmd()
			require.Equal(t, "10", cmd.Flags().Lookup("limit").DefValue)
			require.NoError(t, cmd.ParseFlags([]string{"--limit", limit}))
			err := cmd.PreRunE(cmd, nil)
			if limit == "-1" || limit == "0" {
				require.EqualError(t, err, "--limit must be greater than zero")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPrintWorkspaceGitLog(t *testing.T) {
	commits := []workspaceGitCommit{
		{
			SHA:             "0123456789abcdef0123456789abcdef01234567",
			ShortSHA:        "0123456",
			AuthorName:      "Test Author",
			AuthorEmail:     "author@example.com",
			AuthoredDate:    "2026-09-10T12:00:00Z",
			CommitterName:   "Test Committer",
			CommitterEmail:  "committer@example.com",
			CommittedDate:   "2026-09-10T12:01:00Z",
			Message:         "Add feature\n\nDetails: <tag> and \"quotes\".\n",
			MessageHeadline: "Add feature",
			MessageBody:     "Details: <tag> and \"quotes\".\n",
			ParentSHAs:      []string{"abcdef0123456789abcdef0123456789abcdef0123"},
		},
		{ShortSHA: "abcdef0", MessageHeadline: "Initial commit", ParentSHAs: []string{}},
	}
	t.Run("text", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, printWorkspaceGitLog(&out, commits, false))
		require.Equal(t, "0123456 Add feature\nabcdef0 Initial commit\n", out.String())
	})
	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, printWorkspaceGitLog(&out, commits, true))
		var decoded []workspaceGitCommit
		require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
		require.Equal(t, commits, decoded)
		require.Contains(t, out.String(), `"parentShas":[]`)
	})
	t.Run("empty", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, printWorkspaceGitLog(&out, nil, false))
		require.Empty(t, out.String())
		require.NoError(t, printWorkspaceGitLog(&out, nil, true))
		require.Equal(t, "[]\n", out.String())
	})
	t.Run("write error", func(t *testing.T) {
		for _, jsonOutput := range []bool{false, true} {
			err := errors.New("write failed")
			require.ErrorIs(t, printWorkspaceGitLog(workspaceGitErrorWriter{err}, commits, jsonOutput), err)
		}
	})
}

type workspaceGitErrorWriter struct{ err error }

func (w workspaceGitErrorWriter) Write([]byte) (int, error) { return 0, w.err }
