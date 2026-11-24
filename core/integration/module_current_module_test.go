package core

// These tests cover `dag.CurrentModule()` calls made from inside module code.
// They verify access to the generated call context, installed dependencies,
// module identity, module source, and workdir helpers. They also cover what a
// client outside any function gets from `currentFunctionCall`.
//
// See also:
// - module_self_calls_test.go: modules invoking their own API.
// - module_runtime_behavior_test.go: general module execution behavior.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestCurrentModuleAPI(ctx context.Context, t *testctx.T) {
	t.Run("generatedContextDirectory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module").
			With(daggerCallAt(".", "generated-context-directory", "export", "--path=./out")).
			Directory("out").
			Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dagger.gen.go")
	})

	t.Run("dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module-deps").
			With(daggerCallAt(".", "fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out, "depA,depB")
	})

	t.Run("name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module-name-wacky").
			With(daggerCallAt(".", "fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "WaCkY", strings.TrimSpace(out))
	})

	t.Run("source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module").
			With(daggerCallAt(".", "source-file", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "nice", strings.TrimSpace(out))
	})

	t.Run("contextDirectory and sourceSubpath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithNewFile("shared/value.txt", "from the shared sibling").
			WithNewFile("other/unlisted.txt", "unlisted").
			WithNewFile("m/dagger.json", `{
  "name": "probe",
  "engineVersion": "latest",
  "sdk": {"source": "dang"},
  "include": ["../shared"]
}`).
			WithNewFile("m/main.dang", `type Probe {
  pub sourceSubpath: String! { currentModule.sourceSubpath }
  pub shared: String! { currentModule.contextDirectory.file("shared/value.txt").contents }
  pub contextEntries: [String!]! { currentModule.contextDirectory.entries }
}`)

		out, err := ctr.With(daggerCallAt("./m", "source-subpath")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "m", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("./m", "shared")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "from the shared sibling", strings.TrimSpace(out))

		out, err = ctr.With(daggerCallAt("./m", "context-entries")).Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"m/", "shared/"}, strings.Fields(out))
	})

	t.Run("workdir", func(ctx context.Context, t *testctx.T) {
		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := moduleFixture(t, c, "go/current-module").
				With(daggerCallAt(".", "workdir-dir", "file", "--path=coolfile.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := moduleFixture(t, c, "go/current-module").
				With(daggerCallAt(".", "workdir-file", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("error on escape", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := moduleFixture(t, c, "go/current-module")

			_, err := ctr.
				With(daggerCallAt(".", "escape-file", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCallAt(".", "escape-file-abs", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCallAt(".", "escape-dir", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCallAt(".", "escape-dir-abs", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/foo" escapes workdir`)
		})
	})
}

func (ModuleSuite) TestCurrentFunctionCallOutsideFunction(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "-q", "curl"}).
		WithExec([]string{"sh", "-c", `curl -s -u "$DAGGER_SESSION_TOKEN:" -H 'Content-Type: application/json' ` +
			`-d '{"query":"{currentFunctionCall{name}}"}' "http://127.0.0.1:$DAGGER_SESSION_PORT/query"`}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "no current function call")
}

const currentModuleGitRefProbe = `type Probe {
  pub loaded: String! {
    currentModule.contextDirectory.file("m/dirty.txt").contents
  }

  @cache(policy: FunctionCachePolicy.NEVER)
  pub git: String! {
    let ref = currentModule.gitRef
    if (ref == null) {
      "null"
    } else {
      let changes = ref.asRepository.uncommitted
      ref.commitSha + " head=" + ref.asRepository.head.commitSha +
        " modified=" + changes.modifiedPaths.join(",") +
        " added=" + changes.addedPaths.join(",") +
        " removed=" + changes.removedPaths.join(",") +
        " dirty=" + changes.after.file("m/dirty.txt").contents
    }
  }
}
`

func (ModuleSuite) TestCurrentModuleGitRef(ctx context.Context, t *testctx.T) {
	committedRepo := func(ctx context.Context, t *testctx.T, c *dagger.Client, then string) (*core.Directory, string) {
		ctr := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			With(gitUserConfig).
			WithWorkdir("/repo").
			WithNewFile("m/dagger.json", `{"name": "probe", "engineVersion": "latest", "sdk": {"source": "dang"}, "include": ["!excluded.txt"]}`).
			WithNewFile("m/main.dang", currentModuleGitRefProbe).
			WithNewFile("m/dirty.txt", "v0").
			WithNewFile("m/excluded.txt", "tracked, excluded by the module").
			WithNewFile("outside.txt", "tracked, outside the module").
			WithExec([]string{"sh", "-c", "git init -q && git add -A && git commit -qm module && git rev-parse HEAD > /pinned" + then})
		pinned, err := ctr.File("/pinned").Contents(ctx)
		require.NoError(t, err)
		return ctr.Directory("/repo"), strings.TrimSpace(pinned)
	}
	queryProbe := func(c *dagger.Client, t *testctx.T) (string, string) {
		res, err := testutil.QueryWithClient[struct {
			Probe struct{ Loaded, Git string }
		}](c, t, `{probe{loaded git}}`, nil)
		require.NoError(t, err)
		return res.Probe.Loaded, res.Probe.Git
	}

	t.Run("git", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		repo, pinned := committedRepo(ctx, t, c, " && echo moved > outside.txt && git commit -qam moved")
		gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, repo, "main"), "", nil)
		gitSrv, err := gitSrv.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })
		hostname, err := gitSrv.Hostname(ctx)
		require.NoError(t, err)
		modRef := "http://" + resolveServiceIP(ctx, t, c, hostname) + "/repo.git#" + pinned + ":m"

		out, err := core.NewQuery(c).Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(daggerCallAt(modRef, "git")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, pinned+" head="+pinned+" modified= added= removed= dirty=v0", out)
	})

	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		repo, head := committedRepo(ctx, t, c, "")
		host := t.TempDir()
		_, err := repo.Export(ctx, host)
		require.NoError(t, err)
		write := func(path, contents string) {
			require.NoError(t, os.WriteFile(filepath.Join(host, path), []byte(contents), 0o644))
		}
		write("m/dirty.txt", "v1")
		write("m/new.txt", "untracked")

		require.NoError(t, core.NewQuery(c).ModuleSource(filepath.Join(host, "m")).AsModule().Serve(ctx))
		write("m/dirty.txt", "v2")
		loaded, git := queryProbe(c, t)
		require.Equal(t, "v1", loaded)
		require.Equal(t, head+" head="+head+" modified=m/dirty.txt added=m/new.txt removed= dirty=v2", git)

		write("m/dirty.txt", "v3")
		_, again := queryProbe(c, t)
		require.Equal(t, git, again)
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		repo, _ := committedRepo(ctx, t, c, "")
		require.NoError(t, repo.AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "m"}).AsModule().Serve(ctx))
		_, git := queryProbe(c, t)
		require.Equal(t, "null", git)
	})
}
