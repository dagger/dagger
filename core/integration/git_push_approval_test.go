package core

import (
	"context"
	"os/exec"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestPushModuleRequiresApproval(ctx context.Context, t *testctx.T) {
	// The owner's workspace is a checkout of the service's repository.
	checkout := t.TempDir()
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	service, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base", "base"))
	_, err := service.Start(ctx)
	require.NoError(t, err)
	other, otherURL := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base", "other"))
	_, err = other.Start(ctx)
	require.NoError(t, err)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", url}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	repo := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: service})
	source := repo.Branch("main")
	sha, err := source.CommitSHA(ctx)
	require.NoError(t, err)
	sourceID, err := source.ID(ctx)
	require.NoError(t, err)
	mod := core.NewQuery(c).Directory().
		WithNewFile("dagger.json", `{"name":"pusher","sdk":"go","engineVersion":"v1.0.0-0"}`).
		WithNewFile("main.go", `package main
import (
  "context"
  "dagger/pusher/internal/dagger/core"
)
type Pusher struct{}
func (m *Pusher) Push(ctx context.Context, source *core.GitRef, remote, branch string) (string, error) {
  return source.Push(core.GitRefPushOpts{To: dag.Git(remote), Branch: branch}).Sha(ctx)
}
`)
	require.NoError(t, mod.AsModule().Serve(ctx))
	push := func(remote, branch string) error {
		var response any
		return c.Do(ctx, &dagger.Request{
			Query:     `query($source: ID!, $remote: String!, $branch: String!) { pusher { push(source:$source, remote:$remote, branch:$branch) } }`,
			Variables: map[string]any{"source": sourceID, "remote": remote, "branch": branch},
		}, &dagger.Response{Data: &response})
	}
	// A branch other than the default of the owner's own origin is routine:
	// no approval, so none is needed from this session, which cannot prompt.
	require.NoError(t, push(url, "module-write"))
	require.Equal(t, sha, pushRemoteSHA(ctx, t, c, service, url, "refs/heads/module-write"))
	// The remote's default branch and tags still ask. Anonymous receive-pack
	// must be gated just like authenticated pushes.
	for _, branch := range []string{"main", "refs/tags/module-tag"} {
		err := push(url, branch)
		require.ErrorContains(t, err, "git push requires approval", branch)
		if strings.HasPrefix(branch, "refs/") {
			require.Empty(t, pushRemoteSHA(ctx, t, c, service, url, branch))
		}
	}
	// Any other remote must be approved before its first push.
	require.ErrorContains(t, push(otherURL, "module-write"), "git push requires approval")
	require.Empty(t, pushRemoteSHA(ctx, t, c, other, otherURL, "refs/heads/module-write"))
	// SSH destination construction must also reach the approval gate rather
	// than failing early, or acquiring an owner socket while loading Query.git.
	require.ErrorContains(t, push("git@unreachable.invalid:repo", "module-write"), "git push requires approval")
}
