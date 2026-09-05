package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCommitOptions(t *testing.T) {
	ws := &core.Workspace{Cwd: "src", GitAuthorName: "Loaded Author", GitAuthorEmail: "loaded@example.com"}
	args := workspaceWithCommitArgs{Message: "commit", Date: "2026-09-05T12:00:00Z", Paths: []string{"file", "/root-file"}}
	opts, err := args.opts(ws)
	require.NoError(t, err)
	require.Equal(t, []string{"src/file", "root-file"}, opts.Paths)
	require.Equal(t, ws.GitAuthorName, opts.AuthorName)
	require.Equal(t, ws.GitAuthorEmail, opts.AuthorEmail)
	args.AuthorName = dagql.Opt(dagql.NewString("Explicit Author"))
	args.AuthorEmail = dagql.Opt(dagql.NewString("explicit@example.com"))
	opts, err = args.opts(ws)
	require.NoError(t, err)
	require.Equal(t, "Explicit Author", opts.AuthorName)
	require.Equal(t, "explicit@example.com", opts.AuthorEmail)
	for _, date := range []string{"", "now", "2026-09-05"} {
		args.Date = date
		_, err := args.opts(ws)
		require.ErrorContains(t, err, "RFC3339")
	}
	args.Date = "2026-09-05T12:00:00Z"
	for _, p := range []string{"../../outside", "/.git", "/.git/config"} {
		args.Paths = []string{p}
		_, err := args.opts(ws)
		require.Error(t, err)
	}
}

func TestWorkspaceGitAuthorValidation(t *testing.T) {
	for _, invalid := range []string{"name\ntrailer", "name\rtrailer", "name\x00trailer", "Name <email>"} {
		require.Error(t, validateWorkspaceGitAuthor(invalid, "valid@example.com"))
		require.Error(t, validateWorkspaceGitAuthor("Valid", invalid))
	}
	require.NoError(t, validateWorkspaceGitAuthor("Zoë", "zoe@example.com"))
	require.NoError(t, validateWorkspaceGitAuthor("", ""))
}
