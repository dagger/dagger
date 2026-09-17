package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Loading an SDK through a workspace must use the same cache namespace after
// edits, including when the workspace cwd differs from the config directory.
func (WorkspaceAPISuite) TestSDKCacheNamespaces(ctx context.Context, t *testctx.T) {
	for _, backing := range []string{"local", "git", "directory"} {
		t.Run(backing, func(ctx context.Context, t *testctx.T) {
			key := fmt.Sprintf("workspace-sdk-%d", time.Now().UnixNano())
			source := fmt.Sprintf(`type CacheSdk {
  findClientRoot(ws: Workspace!): String { null }

  generateScope(ws: Workspace!, isModule: Boolean!, name: String!, clients: [ModuleSource!]!): Workspace! {
    let value = Dagger.container.from(%q)
      .withMountedCache("/cache", cacheVolume(%q))
      .withEnvVariable("REVISION", "first")
      .withExec(["sh", "-c", "test -f /cache/value || printf %%s $REVISION > /cache/value; cat /cache/value; printf :%%s $REVISION"])
      .stdout
    ws.withNewFile("cache.txt", value)
      .withNewFile("dagger-module.toml", "name = " + JSON.encode(name) + "\nengineVersion = \"latest\"\n[runtime]\nsource = \"dang\"\n")
      .withNewFile("main.dang", "type App { hello: String! = \"hello\" }\n")
  }
}
`, alpineImage, key)
			files := map[string]string{
				"apps/project/dagger.toml":            "\n",
				"apps/project/sdk/dagger-module.toml": "name = \"cache-sdk\"\nengineVersion = \"latest\"\n[runtime]\nsource = \"dang\"\n",
				"apps/project/sdk/main.dang":          source,
				"apps/project/nested/.keep":           "",
			}
			var ws *dagger.Workspace
			if backing == "local" {
				workdir := t.TempDir()
				initGitRepo(ctx, t, workdir)
				for path, contents := range files {
					path = filepath.Join(workdir, path)
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
					require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				}
				c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(workdir, "apps/project/nested")))
				ws = c.CurrentWorkspace()
			} else {
				c := connect(ctx, t)
				dir := c.Directory()
				for path, contents := range files {
					dir = dir.WithNewFile(path, contents)
				}
				if backing == "git" {
					service, repoURL := gitService(ctx, t, c, dir)
					ws = c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: service}).Head().AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "apps/project/nested"})
				} else {
					ws = dir.AsWorkspace(dagger.DirectoryAsWorkspaceOpts{Cwd: "apps/project/nested"})
				}
			}

			ws = ws.WithSDK("../sdk", dagger.WorkspaceWithSDKOpts{Name: "test"})
			generate := func(ws *dagger.Workspace) *dagger.Workspace {
				return ws.WithInitModule("test", dagger.WorkspaceWithInitModuleOpts{Name: "app", Path: "/apps/project/app"})
			}
			first := generate(ws)
			value, err := first.File("/apps/project/app/cache.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "first:first", value)

			// Changing both source and exec inputs ensures the second call really
			// executes. The suffix proves the edited code ran; a new volume also
			// changes the prefix.
			second := generate(first.WithNewFile("/apps/project/sdk/main.dang", strings.ReplaceAll(source, `"first"`, `"second"`)))
			value, err = second.File("/apps/project/app/cache.txt").Contents(ctx)
			require.NoError(t, err)
			if backing == "directory" {
				// Synthetic directories retain content-based cache identity.
				require.Equal(t, "second:second", value)
			} else {
				require.Equal(t, "first:second", value)
			}
		})
	}
}
