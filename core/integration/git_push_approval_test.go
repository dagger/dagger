package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestPushModuleRequiresApproval(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base", "base"))
	_, err := service.Start(ctx)
	require.NoError(t, err)
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	source := repo.Branch("main")
	sourceID, err := source.ID(ctx)
	require.NoError(t, err)
	mod := c.Directory().
		WithNewFile("dagger.json", `{"name":"pusher","sdk":"go","engineVersion":"v1.0.0-0"}`).
		WithNewFile("main.go", `package main
import (
  "context"
  "dagger/pusher/internal/dagger"
)
type Pusher struct{}
func (m *Pusher) Push(ctx context.Context, source *dagger.GitRef, remote string) (string, error) {
  return source.Push(dagger.GitRefPushOpts{To: dag.Git(remote), Branch: "module-write"}).Sha(ctx)
}
`)
	require.NoError(t, mod.AsModule().Serve(ctx))
	// No interactive prompt attachable is available in this test session.
	// Anonymous receive-pack must be gated just like authenticated pushes.
	var response any
	err = c.Do(ctx, &dagger.Request{
		Query:     `query($source: ID!, $remote: String!) { pusher { push(source:$source, remote:$remote) } }`,
		Variables: map[string]any{"source": sourceID, "remote": url},
	}, &dagger.Response{Data: &response})
	require.ErrorContains(t, err, "git push requires approval")
	require.Empty(t, pushRemoteSHA(ctx, t, c, service, url, "refs/heads/module-write"))
	// SSH destination construction must also reach the approval gate rather
	// than failing early, or acquiring an owner socket while loading Query.git.
	err = c.Do(ctx, &dagger.Request{
		Query:     `query($source: ID!, $remote: String!) { pusher { push(source:$source, remote:$remote) } }`,
		Variables: map[string]any{"source": sourceID, "remote": "git@unreachable.invalid:repo"},
	}, &dagger.Response{Data: &response})
	require.ErrorContains(t, err, "git push requires approval")
}
