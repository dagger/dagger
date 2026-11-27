package core

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

type deniedPushBase interface{ Server }

type deniedPushServer struct {
	deniedPushBase // Any credential, socket, service, or source access would panic.
	calls          int
	remote, ref    string
	force          bool
	lease          string
	resolveURL     bool
	// authorizedLease, when set, authorizes the push with that lease instead
	// of denying it.
	authorizedLease string
}

func (s *deniedPushServer) AuthorizeGitPush(_ context.Context, remote, ref string, force bool, lease string, resolveURL bool) (*GitPushAuthorization, error) {
	s.calls++
	s.remote, s.ref, s.force, s.lease = remote, ref, force, lease
	s.resolveURL = resolveURL
	if s.authorizedLease != "" {
		return &GitPushAuthorization{Remote: remote, Lease: s.authorizedLease}, nil
	}
	return nil, errors.New("permission denied")
}

func TestGitPushRequiresTheAuthorizedLease(t *testing.T) {
	// A leased force-push may be authorized as a plain push would be: a push
	// enforcing another lease, or none, must not run on that authorization.
	srv := &deniedPushServer{authorizedLease: "2222222222222222222222222222222222222222"}
	ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
	url, err := gitutil.ParseURL("https://unreachable.invalid/repo")
	require.NoError(t, err)
	source := &GitRef{Ref: &gitutil.Ref{Name: "refs/heads/feature", SHA: "1111111111111111111111111111111111111111"}}
	for _, expected := range []string{"", "3333333333333333333333333333333333333333"} {
		_, err = source.Push(ctx, &RemoteGitRepository{URL: url}, GitPushOpts{ExpectedRemoteSHA: expected})
		require.ErrorContains(t, err, "authorized for a different lease")
		require.Equal(t, expected, srv.lease)
	}
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
			require.Empty(t, srv.lease)
			_, err = source.Push(ctx, &RemoteGitRepository{URL: url}, GitPushOpts{ExpectedRemoteSHA: source.Ref.SHA})
			require.ErrorContains(t, err, "permission denied")
			require.True(t, srv.force)
			require.Equal(t, source.Ref.SHA, srv.lease, "the lease reaches the policy")
		})
	}
}

func TestGitPushRewriteIsolation(t *testing.T) {
	secret, err := dagql.NewResultForCall(&Secret{}, &dagql.ResultCall{})
	require.NoError(t, err)
	socket, err := dagql.NewResultForCall(&Socket{}, &dagql.ResultCall{})
	require.NoError(t, err)
	for _, tc := range []struct {
		name        string
		destination RemoteGitRepository
		opts        GitPushOpts
		resolve     bool
	}{
		{name: "remote checkout or explicit to", resolve: true},
		{name: "explicit push URL", opts: GitPushOpts{SkipDestinationRewrite: true}},
		{name: "token", destination: RemoteGitRepository{AuthToken: dagql.ObjectResult[*Secret]{Result: secret}}},
		{name: "header", destination: RemoteGitRepository{AuthHeader: dagql.ObjectResult[*Secret]{Result: secret}}},
		{name: "socket", destination: RemoteGitRepository{SSHAuthSocket: dagql.ObjectResult[*Socket]{Result: socket}}},
		{name: "service", destination: RemoteGitRepository{Services: ServiceBindings{{Hostname: "remote"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &deniedPushServer{}
			ctx := ContextWithQuery(t.Context(), &Query{Server: srv})
			url, err := gitutil.ParseURL("https://example.com/repo")
			require.NoError(t, err)
			tc.destination.URL = url
			source := &GitRef{Ref: &gitutil.Ref{Name: "refs/heads/main", SHA: "1111111111111111111111111111111111111111"}}
			_, err = source.Push(ctx, &tc.destination, tc.opts)
			require.ErrorContains(t, err, "permission denied")
			require.Equal(t, tc.resolve, srv.resolveURL)
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
