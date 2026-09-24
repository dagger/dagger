package core

import (
	"context"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceRemoteParentHistoryDoesNotFetch(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", "base\n"))
	remote := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service}).Head()
	base := remote.AsWorkspace()
	working := base.WithNewFile("file.txt", "committed\n")
	id, err := working.WithCommit(working.Git().Uncommitted(), "remote child", workspaceCommitDate, dagger.WorkspaceWithCommitOpts{AuthorName: "Oracle", AuthorEmail: "oracle@example.com"}).ID(ctx)
	require.NoError(t, err)
	child := dagger.Ref[*dagger.Workspace](c, id)
	head := child.Git().Head()
	remoteSHA, err := remote.CommitSHA(ctx)
	require.NoError(t, err)
	_, err = service.Stop(ctx)
	require.NoError(t, err)

	ahead, err := head.Log(ctx, dagger.GitRefLogOpts{Base: remote, Limit: 101})
	require.NoError(t, err)
	require.Len(t, ahead, 1)
	parents, err := ahead[0].ParentShas(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{remoteSHA}, parents)
	behind, err := remote.Log(ctx, dagger.GitRefLogOpts{Base: head, Limit: 101})
	require.NoError(t, err)
	require.Empty(t, behind)
	// A different tree selector forces another incremental materialization;
	// its canonical parent dependency must work without restarting the source.
	contents, err := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true, Depth: -1}).File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "committed\n", contents)
	gotSHA, err := remote.CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, remoteSHA, gotSHA, "operation-local substitution must not mutate public refs")
	require.NoError(t, c.Close())

	traces, _ := sink.capture()
	names, parentIDs := map[string]string{}, map[string]string{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					names[id], parentIDs[id] = span.Name, string(span.TraceId)+string(span.ParentSpanId)
				}
			}
		}
	}
	walks := 0
	for id, name := range names {
		for parent := parentIDs[id]; parent != ""; parent = parentIDs[parent] {
			if names[parent] != "GitRef.log" {
				continue
			}
			require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "exact remote/local parent comparison fetched: %s", name)
			require.NotEqual(t, "service.start", name, "history comparison restarted the stopped remote service")
			if strings.HasPrefix(name, "git rev-list") && names[parentIDs[id]] != name {
				walks++
			}
			break
		}
	}
	require.GreaterOrEqual(t, walks, 2, "both ahead and behind must actually traverse history")
}
