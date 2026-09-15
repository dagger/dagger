package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestGitRepositoryRemotesPersistence(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	dir := volumeTestCachedObjectResult(t, ctx, cache, srv, "push-routing", "git-directory", &Directory{})
	for _, remotes := range [][]GitRemote{
		nil,
		{
			{Name: "origin", URL: "https://fetch.test/repo", PushURLs: []string{"ssh://git@example.test/repo", "https://other.test/repo"}},
			{Name: "upstream", URL: "https://upstream.test/repo"},
		},
	} {
		repo := &GitRepository{
			URL:     dagql.NonNull(dagql.String("https://fetch.test/repo")),
			Remotes: remotes,
			Backend: &LocalGitRepository{Directory: dir},
		}
		encoded, err := repo.EncodePersistedObject(ctx, cache)
		require.NoError(t, err)
		decoded, err := (&GitRepository{}).DecodePersistedObject(ctx, srv, 0, nil, encoded.JSON)
		require.NoError(t, err)
		restored := decoded.(*GitRepository)
		require.Equal(t, repo.URL, restored.URL)
		require.Equal(t, repo.Remotes, restored.Remotes)
		require.IsType(t, &LocalGitRepository{}, restored.Backend)
	}
}
