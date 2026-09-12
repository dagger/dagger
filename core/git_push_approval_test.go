package core

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

type deniedPushBase interface{ Server }

type deniedPushServer struct {
	deniedPushBase // Any credential, socket, service, or source access would panic.
	calls          int
	remote, ref    string
	force          bool
}

func (s *deniedPushServer) AuthorizeGitPush(_ context.Context, remote, ref string, force bool) (*engine.ClientMetadata, error) {
	s.calls++
	s.remote, s.ref, s.force = remote, ref, force
	return nil, errors.New("permission denied")
}

func TestGitPushApprovalBeforeDestinationAccess(t *testing.T) {
	for _, remote := range []string{"git://unreachable.invalid/repo", "https://unreachable.invalid/repo", "git@unreachable.invalid:repo"} {
		t.Run(remote, func(t *testing.T) {
			srv := &deniedPushServer{}
			ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
			url, err := gitutil.ParseURL(remote)
			require.NoError(t, err)
			source := &GitRef{Ref: &gitutil.Ref{Name: "refs/heads/main", SHA: "1111111111111111111111111111111111111111"}}
			_, err = source.Push(ctx, &RemoteGitRepository{URL: url}, GitPushOpts{})
			require.ErrorContains(t, err, "permission denied")
			require.Equal(t, 1, srv.calls)
			require.Equal(t, remote, srv.remote)
			require.Equal(t, "refs/heads/main", srv.ref)
			require.False(t, srv.force)
			_, err = source.Push(ctx, &RemoteGitRepository{URL: url}, GitPushOpts{ExpectedRemoteSHA: source.Ref.SHA})
			require.ErrorContains(t, err, "permission denied")
			require.True(t, srv.force)
		})
	}
}

func TestGitPushRejectsEmbeddedCredentials(t *testing.T) {
	for _, remote := range []string{"https://sensitive-token@example.com/repo", "ssh://git:sensitive-password@example.com/repo"} {
		srv := &deniedPushServer{}
		ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
		url, err := gitutil.ParseURL(remote)
		require.NoError(t, err)
		source := &GitRef{Ref: &gitutil.Ref{Name: "refs/heads/main", SHA: "1111111111111111111111111111111111111111"}}
		_, err = source.Push(ctx, &RemoteGitRepository{URL: url}, GitPushOpts{})
		require.ErrorContains(t, err, "embedded credentials")
		require.NotContains(t, err.Error(), "sensitive")
		require.Zero(t, srv.calls)
	}
}
