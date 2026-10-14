package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// sourcesProbeEntrypoint is a module entrypoint with a sources member, spliced
// in with %s. Its types and call keep a nullable workspace argument, as the Go
// SDK generates them, and fail when the engine passes one. Nonce returns the
// shared file and a random string, so it repeats exactly when the engine
// reuses the call result.
const sourcesProbeEntrypoint = `type Entrypoint implements ModuleEntrypoint {
%s

  pub types(workspace: Workspace): [TypeDef!]! {
    if (workspace != null) {
      raise "types received a workspace"
    }
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("ContextFiles", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Shared", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Nonce", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(
    workspace: Workspace,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! {
    if (workspace != null) {
      raise "call received a workspace"
    }
    if (fnName == "") {
      ("{}" :: JSON!)
    } else if (fnName == "ContextFiles") {
      (JSON.encode(currentModule.contextDirectory.glob("**").join("\n")) :: JSON!)
    } else if (fnName == "Shared") {
      (JSON.encode(shared) :: JSON!)
    } else if (fnName == "Nonce") {
      (JSON.encode(shared.trimSpace + "@" + Random.string) :: JSON!)
    } else {
      raise "unknown function: " + fnName
    }
  }

  let shared: String! {
    currentModule.contextDirectory.file(currentModule.sourceSubpath + "/../shared/value.txt").contents
  }
}
`

const sharedSourcesMember = `  pub sources(workspace: Workspace!): Directory! {
    directory
      .withDirectory("shared", workspace.directory("/shared", exclude: ["testdata"]))
      .withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
  }`

const sourcesModuleManifest = `name = "m"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`

func sourcesModuleFixture(c *dagger.Client, member string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("shared/value.txt", "from the shared sibling\n").
		WithNewFile("shared/testdata/big.txt", "big\n").
		WithNewFile("other/unlisted.txt", "unlisted\n").
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/data/local.txt", "module data\n").
		WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(sourcesProbeEntrypoint, member))
}

func moduleSourceContextFiles(ctx context.Context, ctr *core.Container, ref string) ([]string, error) {
	out, err := ctr.
		With(daggerExec("core", "module-source", "--ref-string", ref, "context-directory", "glob", "--pattern", "**")).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func moduleSourceDigest(ctx context.Context, t *testctx.T, ctr *core.Container, ref string) string {
	t.Helper()
	out, err := ctr.With(daggerExec("core", "module-source", "--ref-string", ref, "digest")).Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func requireNoGitMetadata(t *testctx.T, files []string) {
	t.Helper()
	for _, file := range files {
		for _, part := range strings.Split(strings.TrimSuffix(file, "/"), "/") {
			require.NotEqual(t, ".git", part, "%s is repository metadata", file)
		}
	}
}

func (ModuleSuite) TestModuleEntrypointSources(ctx context.Context, t *testctx.T) {
	local := func(t *testctx.T, c *dagger.Client, member string) *core.Container {
		return goGitBase(t, c).WithDirectory(".", sourcesModuleFixture(c, member))
	}
	call := func(ctx context.Context, t *testctx.T, ctr *core.Container, ref, fn string) string {
		t.Helper()
		out, err := ctr.With(daggerCallAt(ref, fn)).Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	requireLoaded := func(t *testctx.T, files []string) {
		t.Helper()
		require.Contains(t, files, "m/dagger-module.toml")
		require.Contains(t, files, "m/entrypoint/main.dang")
		require.Contains(t, files, "m/data/local.txt")
		require.Contains(t, files, "shared/value.txt")
		require.NotContains(t, files, "shared/testdata/big.txt")
		require.NotContains(t, files, "other/unlisted.txt")
		requireNoGitMetadata(t, files)
	}

	t.Run("local module loads the returned files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedSourcesMember)

		files, err := moduleSourceContextFiles(ctx, ctr, "./m")
		require.NoError(t, err)
		requireLoaded(t, files)
		requireLoaded(t, strings.Fields(call(ctx, t, ctr, "./m", "context-files")))
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, "./m", "shared"))
	})

	t.Run("workspace module loads the returned files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedSourcesMember).
			WithNewFile("dagger.toml", "[modules.m]\nsource = \"m\"\n")

		requireLoaded(t, strings.Fields(call(ctx, t, ctr, "m", "context-files")))
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, "m", "shared"))
	})

	// The workspace that sources gets reads the module's own checkout through
	// the client that loaded it, also when that checkout is not the caller's
	// workspace.
	t.Run("module by path from another repository", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			WithNewFile("shared/value.txt", "the caller's copy\n").
			WithDirectory("/other", sourcesModuleFixture(c, sharedSourcesMember)).
			WithExec([]string{"git", "init", "-q", "/other"})

		requireLoaded(t, strings.Fields(call(ctx, t, ctr, "/other/m", "context-files")))
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, "/other/m", "shared"))
	})

	t.Run("git module reads its own commit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		gitDaemon, repoURL := gitService(ctx, t, c, sourcesModuleFixture(c, sharedSourcesMember))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		ctr := goGitBase(t, c).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("shared/value.txt", "the caller's copy\n")

		files, err := moduleSourceContextFiles(ctx, ctr, repoURL+"#main:m")
		require.NoError(t, err)
		requireLoaded(t, files)
		require.Equal(t, "from the shared sibling", call(ctx, t, ctr, repoURL+"#main:m", "shared"))
	})

	t.Run("directory module loads the returned files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedSourcesMember)

		out, err := ctr.With(daggerQuery(`{host{directory(path:"."){asModuleSource(sourceRootPath:"m"){contextDirectory{glob(pattern:"**")}}}}}`)).Stdout(ctx)
		require.NoError(t, err)
		var res struct {
			Host struct {
				Directory struct {
					AsModuleSource struct {
						ContextDirectory struct {
							Glob []string
						}
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal([]byte(out), &res))
		requireLoaded(t, res.Host.Directory.AsModuleSource.ContextDirectory.Glob)
	})

	t.Run("a context reload keeps the returned files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedSourcesMember)

		out, err := ctr.With(daggerQuery(`{moduleSource(refString:"./m"){withIncludes(patterns:["data"]){contextDirectory{glob(pattern:"**")}}}}`)).Stdout(ctx)
		require.NoError(t, err)
		var res struct {
			ModuleSource struct {
				WithIncludes struct {
					ContextDirectory struct {
						Glob []string
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal([]byte(out), &res))
		requireLoaded(t, res.ModuleSource.WithIncludes.ContextDirectory.Glob)
	})

	t.Run("digest follows the returned files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedSourcesMember)
		original := moduleSourceDigest(ctx, t, ctr, "./m")

		ctr = ctr.WithNewFile("shared/value.txt", "changed\n")
		changed := moduleSourceDigest(ctx, t, ctr, "./m")
		require.NotEqual(t, original, changed)

		ctr = ctr.WithNewFile("other/unlisted.txt", "changed\n")
		require.Equal(t, changed, moduleSourceDigest(ctx, t, ctr, "./m"))
	})

	// Each dagger call is a new client and session. The engine evaluates
	// sources in every session, and the default cache policy reuses a result
	// while the returned files are unchanged.
	t.Run("results are reused across sessions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := local(t, c, sharedSourcesMember)
		nonce := func(ctr *core.Container, run string) string {
			return call(ctx, t, ctr.WithEnvVariable("RUN", run), "./m", "nonce")
		}

		first := nonce(ctr, "1")
		require.True(t, strings.HasPrefix(first, "from the shared sibling@"), first)
		require.Equal(t, first, nonce(ctr, "2"))

		ctr = ctr.WithNewFile("shared/value.txt", "changed\n")
		changed := nonce(ctr, "3")
		require.True(t, strings.HasPrefix(changed, "changed@"), changed)
		require.Equal(t, changed, nonce(ctr, "4"))

		ctr = ctr.WithNewFile("other/unlisted.txt", "changed\n")
		require.Equal(t, changed, nonce(ctr, "5"), "a file outside the returned directory keeps the result")

		ctr = ctr.WithNewFile("m/data/local.txt", "changed\n")
		require.NotEqual(t, changed, nonce(ctr, "6"), "a module file changes the result")
	})

	// The engine adds the module context it loads without the member, so a Git
	// module keeps a committed file that .gitignore lists, and a local module
	// drops an ignored file that sources reads with gitignore.
	t.Run(".gitignore", func(ctx context.Context, t *testctx.T) {
		t.Run("git module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			repo := core.NewQuery(c).Container().From(alpineImage).
				WithExec([]string{"apk", "add", "git"}).
				WithDirectory("/repo", sourcesModuleFixture(c, sharedSourcesMember).
					WithNewFile(".gitignore", "generated.txt\n").
					WithNewFile("m/generated.txt", "committed anyway\n")).
				WithWorkdir("/repo").
				WithExec([]string{"sh", "-c", `set -e
git init -q -b main
git config user.email dagger@example.com
git config user.name "Dagger Tests"
git add -A
git add -f m/generated.txt
git commit -qm init
`}).
				Directory("/repo")
			gitDaemon, repoURL := gitService(ctx, t, c, repo)
			gitHost, err := gitDaemon.Hostname(ctx)
			require.NoError(t, err)
			ctr := goGitBase(t, c).WithServiceBinding(gitHost, gitDaemon)

			files, err := moduleSourceContextFiles(ctx, ctr, repoURL+"#main:m")
			require.NoError(t, err)
			require.Contains(t, files, "m/generated.txt")
			require.Contains(t, files, "shared/value.txt")
		})

		t.Run("local module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := local(t, c, sharedSourcesMember).
				WithNewFile(".gitignore", "generated.txt\n").
				WithNewFile("m/generated.txt", "ignored\n")

			files, err := moduleSourceContextFiles(ctx, ctr, "./m")
			require.NoError(t, err)
			require.NotContains(t, files, "m/generated.txt")
			require.Contains(t, files, "shared/value.txt")
		})
	})

	t.Run("refusals", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			name   string
			member string
			err    string
		}{
			{
				name:   "raised error",
				member: `  pub sources(workspace: Workspace!): Directory! { raise "no sources today" }`,
				err:    "no sources today",
			},
			{
				name:   "directory rooted at the module",
				member: `  pub sources(workspace: Workspace!): Directory! { workspace.directory(".") }`,
				err:    "the returned directory has no m/dagger-module.toml",
			},
			{
				name:   "read above the workspace root",
				member: `  pub sources(workspace: Workspace!): Directory! { workspace.directory("../..") }`,
				err:    "escapes workspace root",
			},
			{
				name:   "wrong return type",
				member: `  pub sources(workspace: Workspace!): String! { "../shared" }`,
				err:    `field "sources": return type`,
			},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				_, err := moduleSourceContextFiles(ctx, local(t, c, tc.member), "./m")
				requireErrOut(t, err, tc.err)
			})
		}

		// The engine evaluates sources while it loads the module source, from
		// the entrypoint in the module's context.
		t.Run("entrypoint named by module reference", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			gitDaemon, repoURL := gitService(ctx, t, c, core.NewQuery(c).Directory().
				WithNewFile("entry/main.dang", fmt.Sprintf(sourcesProbeEntrypoint, sharedSourcesMember)))
			gitHost, err := gitDaemon.Hostname(ctx)
			require.NoError(t, err)
			ctr := local(t, c, "").
				WithServiceBinding(gitHost, gitDaemon).
				WithNewFile("m/dagger-module.toml", `name = "m"

[entrypoint]
kind = "dang"
source = "`+repoURL+`#main:entry"
`)

			_, err = ctr.With(daggerCallAt("./m", "shared")).Stdout(ctx)
			requireErrOut(t, err, "sources needs a local entrypoint")
		})
	})

	t.Run("repository metadata stays out", func(ctx context.Context, t *testctx.T) {
		broad := `  pub sources(workspace: Workspace!): Directory! { workspace.directory("/") }`

		t.Run("nested repository", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := local(t, c, broad).WithExec([]string{"git", "init", "-q", "shared/vendor"})

			files, err := moduleSourceContextFiles(ctx, ctr, "./m")
			require.NoError(t, err)
			require.Contains(t, files, "other/unlisted.txt")
			require.Contains(t, files, "shared/vendor/")
			requireNoGitMetadata(t, files)
		})

		t.Run("worktree whose .git is a file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := local(t, c, broad).
				WithExec([]string{"sh", "-c", "mv .git /repo.git && echo 'gitdir: /repo.git' > .git"})

			files, err := moduleSourceContextFiles(ctx, ctr, "./m")
			require.NoError(t, err)
			require.Contains(t, files, "other/unlisted.txt")
			requireNoGitMetadata(t, files)
		})

		t.Run("root module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := goGitBase(t, c).
				WithNewFile("dagger-module.toml", sourcesModuleManifest).
				WithNewFile("shared/value.txt", "at the root\n").
				WithNewFile("entrypoint/main.dang", fmt.Sprintf(sourcesProbeEntrypoint, broad))

			files, err := moduleSourceContextFiles(ctx, ctr, ".")
			require.NoError(t, err)
			require.Contains(t, files, "entrypoint/main.dang")
			requireNoGitMetadata(t, files)
		})
	})
}

// A module without sources keeps the contract that passes the module
// workspace to types and call.
func (ModuleSuite) TestModuleEntrypointWithoutSourcesGetsWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
		WithNewFile("m/entrypoint/main.dang", `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace): [TypeDef!]! {
    if (workspace == null) {
      raise "types received no workspace"
    }
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("Cwd", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      (JSON.encode(workspace.cwd) :: JSON!)
    }
  }
}
`)

	out, err := ctr.With(daggerCallAt("./m", "cwd")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "/m", strings.TrimSpace(out))
}

// A module serves its declared clients after the engine has looked up the
// calling function, so the target's returned files must reach the caller's
// call key through the target's digest.
func (ModuleSuite) TestModuleEntrypointSourcesDeclaredClient(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithDirectory(".", sourcesModuleFixture(c, sharedSourcesMember)).
		WithNewFile("dagger.toml", `[modules.caller]
source = ".dagger/modules/caller"

[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes.".dagger/modules/caller"]
is-module = true
clients = ["./m"]
`).
		WithNewFile(".dagger/modules/caller/dagger.json", `{"name":"caller","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
		WithNewFile(".dagger/modules/caller/main.go", `package main

import (
	"context"
	"strconv"
	"time"
)

type Caller struct{}

func (m *Caller) Message(ctx context.Context) (string, error) {
	if err := dag.ServeModule(ctx, "/m"); err != nil {
		return "", err
	}
	var message string
	q := dag.QueryBuilder().Select("m").Select("shared").Bind(&message)
	if err := q.Execute(ctx); err != nil {
		return "", err
	}
	return message + " at " + strconv.FormatInt(time.Now().UnixNano(), 10), nil
}

func (m *Caller) Target(ctx context.Context, fn string, run string) (string, error) {
	if err := dag.ServeModule(ctx, "/m"); err != nil {
		return "", err
	}
	var out string
	q := dag.QueryBuilder().Select("m").Select(fn).Bind(&out)
	return out, q.Execute(ctx)
}
`)
	call := func(ctr *core.Container, run string) string {
		out, err := ctr.
			WithEnvVariable("SOURCES_RUN", run).
			With(daggerCallAt("caller", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	// The caller serves the target from the caller's own tree, so the engine
	// loads the target from a directory that holds more than it returns. run
	// makes the caller run again, while the target's result is reused.
	target := func(ctr *core.Container, fn, run string) string {
		out, err := ctr.With(daggerCallAt("caller", "target", "--fn", fn, "--run", run)).Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	files := strings.Fields(target(ctr, "contextFiles", "1"))
	require.Contains(t, files, "shared/value.txt")
	require.NotContains(t, files, "other/unlisted.txt")
	served := target(ctr, "nonce", "1")
	require.Equal(t, served, target(ctr.WithNewFile("other/unlisted.txt", "served\n"), "nonce", "2"),
		"a file outside the target's returned directory keeps the served target's result")

	first := call(ctr, "1")
	require.Contains(t, first, "from the shared sibling\n at ")
	require.Equal(t, first, call(ctr, "2"), "an unchanged target keeps the caller's result")

	ctr = ctr.WithNewFile("shared/value.txt", "changed\n")
	changed := call(ctr, "3")
	require.Contains(t, changed, "changed\n at ")
	require.Equal(t, changed, call(ctr, "4"))

	ctr = ctr.WithNewFile("other/unlisted.txt", "changed\n")
	require.Equal(t, changed, call(ctr, "5"), "a file outside the target's returned directory keeps the caller's result")
}
