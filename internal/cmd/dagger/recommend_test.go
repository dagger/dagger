package daggercmd

import (
	"context"
	"net/http"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestSimpleRecommend(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })

	dir := core.NewQuery(dag).Directory().
		WithNewFile("go.mod", "module example.com/root").
		WithNewFile("app/go.mod", "module example.com/app").
		WithNewFile("app/go.sum", "")
	for _, excluded := range []string{".git", ".dagger", "node_modules", "vendor", "dist", "build", "target"} {
		dir = dir.
			WithNewFile(excluded+"/go.mod", "module example.com/excluded").
			WithNewFile("app/"+excluded+"/go.mod", "module example.com/excluded")
	}
	ws := dir.AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "/app"})

	matches, err := SimpleRecommend("**/go.mod", "**/go.sum")(ctx, ws)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"go.mod", "app/go.mod", "app/go.sum"}, matches)

	matches, err = SimpleRecommend("**/missing.toml")(ctx, ws)
	require.NoError(t, err)
	require.Empty(t, matches)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = SimpleRecommend("**/go.mod")(canceled, ws)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRecommendModules(t *testing.T) {
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })
	ws := core.NewQuery(dag).Directory().WithNewDirectory("app").AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "/app"})

	mods := []registryModule{
		{Name: "z", Recommend: func(ctx context.Context, ws *core.Workspace) ([]string, error) {
			cwd, err := ws.Cwd(ctx)
			require.NoError(t, err)
			require.Equal(t, "/", cwd)
			return []string{"z/config", "a/config", "a/config"}, nil
		}},
		{Name: "installed", Recommend: func(context.Context, *core.Workspace) ([]string, error) {
			t.Fatal("installed modules must not be scanned")
			return nil, nil
		}},
		{Name: "disabled"},
		{Name: "empty", Recommend: func(context.Context, *core.Workspace) ([]string, error) {
			return nil, nil
		}},
		{Name: "a", Recommend: func(context.Context, *core.Workspace) ([]string, error) {
			return []string{"other/config"}, nil
		}},
	}
	recs, err := recommendModules(ctx, ws, mods, map[string]bool{"installed": true})
	require.NoError(t, err)
	require.Len(t, recs, 2)
	require.Equal(t, "a", recs[0].Module.Name)
	require.Equal(t, "other/config", recs[0].Match)
	require.Equal(t, "z", recs[1].Module.Name)
	require.Equal(t, "a/config", recs[1].Match)

	_, err = recommendModules(ctx, ws, []registryModule{{
		Name: "canceled",
		Recommend: func(context.Context, *core.Workspace) ([]string, error) {
			return nil, context.Canceled
		},
	}}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

// Without the module catalog (no cache and a failed fetch), dagger init says
// so in one line and carries on, while dagger module recommend fails, naming
// the URL and the cause. Neither touches the engine first.
func TestPlanRecommendCatalogUnavailable(t *testing.T) {
	srv := serveModuleCatalog(t, http.StatusBadGateway, "")
	withModuleCatalogSource(t, srv.URL)

	t.Run("init carries on", func(t *testing.T) {
		recs, install, err := planRecommend(t.Context(), nil, nil, true)
		require.NoError(t, err)
		require.Empty(t, recs)
		require.False(t, install)
	})

	t.Run("init line", func(t *testing.T) {
		_, err := loadModuleRegistry(t.Context())
		var unavailable *moduleCatalogUnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.Equal(t,
			"Module recommendations unavailable: couldn't fetch "+srv.URL+": unexpected status 502 Bad Gateway.",
			recommendationsUnavailableMessage(unavailable))
	})

	t.Run("module recommend fails", func(t *testing.T) {
		_, _, err := planRecommend(t.Context(), nil, nil, false)
		require.EqualError(t, err, "module catalog unavailable: couldn't fetch "+srv.URL+": unexpected status 502 Bad Gateway")
	})

	t.Run("cancellation aborts init", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := planRecommend(ctx, nil, nil, true)
		require.ErrorIs(t, err, context.Canceled)
	})
}
