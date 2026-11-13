package schema

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCommitOptions(t *testing.T) {
	args := workspaceWithCommitArgs{Message: "commit", Date: "2026-09-05T12:00:00Z", AuthorName: dagql.Opt(dagql.NewString("Explicit Author")), AuthorEmail: dagql.Opt(dagql.NewString("explicit@example.com"))}
	opts, err := args.opts()
	require.NoError(t, err)
	require.Equal(t, "Explicit Author", opts.AuthorName)
	require.Equal(t, "explicit@example.com", opts.AuthorEmail)
	args.AuthorName = dagql.Optional[dagql.String]{}
	args.AuthorEmail = dagql.Optional[dagql.String]{}
	fallback, err := args.opts()
	require.NoError(t, err)
	require.Equal(t, "Dagger", fallback.AuthorName)
	require.Equal(t, "dagger@localhost", fallback.AuthorEmail)
	for _, date := range []string{"", "now", "2026-09-05"} {
		args.Date = date
		_, err := args.opts()
		require.ErrorContains(t, err, "RFC3339")
	}
	args.Date = "2026-09-05T12:00:00Z"
	for _, message := range []string{"", " \n", "subject\x00body"} {
		args.Message = message
		_, err := args.opts()
		require.ErrorContains(t, err, "message must be nonempty and contain no NUL")
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

type gitIdentityServerBase interface{ core.Server }

type gitIdentityServer struct {
	gitIdentityServerBase // Anything but AuthorizeGitIdentity would panic.
	identity              core.GitIdentity
	err                   error
}

func (s *gitIdentityServer) AuthorizeGitIdentity(context.Context) (core.GitIdentity, error) {
	return s.identity, s.err
}

func TestWorkspaceGitAuthor(t *testing.T) {
	for _, tc := range []struct {
		name              string
		srv               *gitIdentityServer
		wantName, wantErr string
		wantEmail         string
	}{
		{name: "shared", srv: &gitIdentityServer{identity: core.GitIdentity{Name: "Personal", Email: "personal@example.com"}}, wantName: "Personal", wantEmail: "personal@example.com"},
		{name: "partial", srv: &gitIdentityServer{identity: core.GitIdentity{Email: "personal@example.com"}}, wantName: "Dagger", wantEmail: "personal@example.com"},
		{name: "unconfigured", srv: &gitIdentityServer{}, wantName: "Dagger", wantEmail: "dagger@localhost"},
		// Not sharing means "don't attribute to me", not "don't commit".
		{name: "not shared", srv: &gitIdentityServer{identity: core.GitIdentity{Name: "Leaked"}, err: fmt.Errorf("%w: denied", core.ErrGitIdentityNotShared)}, wantName: "Dagger", wantEmail: "dagger@localhost"},
		{name: "failed", srv: &gitIdentityServer{err: errors.New("prompt failed")}, wantErr: "prompt failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := core.ContextWithQuery(t.Context(), &core.Query{Server: tc.srv})
			name, email, err := workspaceGitAuthor(ctx)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantName, name)
			require.Equal(t, tc.wantEmail, email)
		})
	}
}
