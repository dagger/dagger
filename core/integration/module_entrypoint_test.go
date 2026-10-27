package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestBuiltinDangModuleEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "entrypoint"
`).
		WithDirectory(
			".dagger/modules/tiny/entrypoint",
			core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		With(daggerCallAt("tiny", "hello"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

func (ModuleSuite) TestBuiltinDangModuleEntrypointFromSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory(
			".dagger/modules/tiny/entrypoint",
			core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		WithNewFile("sub/dir/.keep", "").
		WithWorkdir("sub/dir").
		With(daggerCallAt("tiny", "hello"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

func (ModuleSuite) TestDangModuleEntrypointEnumRoundtrip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.tiny]\nsource = \"tiny\"\n").
		WithNewFile("tiny/dagger-module.toml", "name = \"tiny\"\n[entrypoint]\nkind = \"dang\"\nsource = \"./entrypoint\"\n").
		WithNewFile("tiny/entrypoint/main.dang", `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef.withEnum("Status").withEnumMember("Ready", value: "ready"),
      typeDef.withObject("Tiny")
        .withConstructor(function("", typeDef.withObject("Tiny")))
        .withFunction(function("State", typeDef.withEnum("Status"))
          .withArg("status", typeDef.withEnum("Status"))),
    ]
  }
  pub call(workspace: Workspace!, receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    if (fnName == "") { "{}" :: JSON! }
    else {
      if (fnArgs != ("{\"status\":\"Ready\"}" :: JSON!)) {
        raise "the owning module must receive the original enum name"
      }
      "\"Ready\"" :: JSON!
    }
  }
}`).
		With(daggerCallAt("tiny", "state", "--status", "READY"))
	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "READY", strings.TrimSpace(out))
}

// The engine records which interface it drove a module through, but the span is
// internal: it belongs in a trace, not in the output of an ordinary call.
func (ModuleSuite) TestModuleEntrypointInterfaceSpanIsInternal(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
		WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory(
			".dagger/modules/app/entrypoint",
			core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		With(daggerCallAt("app", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))

	require.NoError(t, c.Close()) // close + flush logs
	require.NotContains(t, logs.String(), "module entrypoint interface")
	require.NotContains(t, logs.String(), "legacy runtime interface")
}

// entrypoint.source can be a module reference, resolved the way runtime.source
// is. One entrypoint served from a git repository can then back many modules,
// with nothing generated into them.
func (ModuleSuite) TestDangModuleEntrypointFromModuleRef(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The served repository holds only Dang files under entrypoint/: an
	// entrypoint directory is not a module and carries no manifest.
	served := core.NewQuery(c).Directory().WithDirectory(
		"entrypoint",
		core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint"),
	)
	gitDaemon, repoURL := gitService(ctx, t, c, served)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	out, err := goGitBase(t, c).
		WithServiceBinding(gitHost, gitDaemon).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "`+repoURL+`#main:entrypoint"
`).
		With(daggerCallAt("tiny", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

// An entrypoint directory usually lives inside the repository of the SDK that
// owns it, below that SDK's own module manifest. The reference names the
// entrypoint directory, so the engine must not walk up to the enclosing module
// and evaluate that module's Dang files instead.
func (ModuleSuite) TestDangModuleEntrypointFromModuleRefInsideModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The root module's Dang file does not type check on its own. Evaluating it
	// as the entrypoint fails, so the test passes only when the engine reads
	// entrypoint/ and nothing above it.
	served := core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", `name = "owner"

[runtime]
source = "dang"
`).
		WithNewFile("main.dang", `type Owner {
  pub broken: String! {
    missingDependency.value
  }
}
`).
		WithDirectory(
			"entrypoint",
			core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint"),
		)
	gitDaemon, repoURL := gitService(ctx, t, c, served)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	out, err := goGitBase(t, c).
		WithServiceBinding(gitHost, gitDaemon).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "`+repoURL+`#main:entrypoint"
`).
		With(daggerCallAt("tiny", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

func (ModuleSuite) TestDangModuleEntrypointAtGitRoot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	mod := core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory("entrypoint", core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint")).
		WithNewFile("marker.txt", "marker\n")
	gitDaemon, repoURL := gitService(ctx, t, c, mod.WithDirectory("sub/tiny", mod))
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	base := goGitBase(t, c).WithServiceBinding(gitHost, gitDaemon)

	for name, ref := range map[string]string{"root": repoURL + "#main", "subpath": repoURL + "#main:sub/tiny"} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			entries, err := base.
				With(daggerExec("core", "module-source", "--ref-string", ref, "directory", "--path", ".", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, entries, "entrypoint/")
			require.Contains(t, entries, "marker.txt")

			out, err := base.With(daggerCallAt(ref, "hello")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		})
	}
}

func (ModuleSuite) TestModuleAtLocalRepoRoot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := goGitBase(t, c).
		WithNewFile(".gitignore", "ignored.txt\n").
		WithNewFile("ignored.txt", "ignored\n").
		WithNewFile("marker.txt", "marker\n")
	contextFiles := func(ctx context.Context, t *testctx.T, ctr *core.Container) string {
		t.Helper()
		out, err := ctr.
			With(daggerExec("core", "module-source", "--ref-string", ".", "context-directory", "glob", "--pattern", "**")).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	t.Run("entrypoint module", func(ctx context.Context, t *testctx.T) {
		ctr := base.
			WithNewFile("dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
			WithDirectory("entrypoint", core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint"))

		files := contextFiles(ctx, t, ctr)
		require.Contains(t, files, "entrypoint/main.dang")
		require.Contains(t, files, "marker.txt")
		require.NotContains(t, files, "ignored.txt")

		out, err := ctr.With(daggerCallAt(".", "hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})

	// A manifest v2 module ignores include, so only a module without an SDK
	// takes this path with include patterns.
	t.Run("module without an SDK", func(ctx context.Context, t *testctx.T) {
		files := contextFiles(ctx, t, base.
			WithNewFile("dagger-module.toml", `name = "plain"
engineVersion = "latest"
include = ["keep/**", "!keep/drop/**"]
`).
			WithNewFile("keep/kept.txt", "kept\n").
			WithNewFile("keep/drop/dropped.txt", "dropped\n"))
		require.Contains(t, files, "marker.txt")
		require.Contains(t, files, "keep/kept.txt")
		require.NotContains(t, files, "dropped.txt")
		require.NotContains(t, files, "ignored.txt")
	})
}

func moduleContextFixture(c *dagger.Client, entrypointDir string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("shared/value.txt", "from the shared sibling\n").
		WithNewFile("other/unlisted.txt", "unlisted\n").
		WithNewFile("m/dagger-module.toml", `name = "m"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory("m/entrypoint", core.NewQuery(c).Host().Directory(entrypointDir))
}

// An entrypoint finds its module inside currentModule.contextDirectory through
// currentModule.sourceSubpath. currentModule.source stays narrowed to the
// module directory.
func (ModuleSuite) TestModuleEntrypointContextDirectory(ctx context.Context, t *testctx.T) {
	probeFixture := func(c *dagger.Client) *core.Directory {
		return moduleContextFixture(c, "./testdata/modules/dang/module-entrypoint-context")
	}
	call := func(ctx context.Context, t *testctx.T, ctr *core.Container, ref, fn string) string {
		t.Helper()
		out, err := ctr.With(daggerCallAt(ref, fn)).Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	requireModuleContext := func(ctx context.Context, t *testctx.T, ctr *core.Container, ref string) {
		t.Helper()
		require.Equal(t, "m", call(ctx, t, ctr, ref, "source-subpath"))

		contextFiles := strings.Fields(call(ctx, t, ctr, ref, "context-files"))
		require.Contains(t, contextFiles, "m/dagger-module.toml")
		require.Contains(t, contextFiles, "m/entrypoint/main.dang")
		for _, file := range contextFiles {
			require.True(t, strings.HasPrefix(file, "m/"), "%s is outside the module directory", file)
		}

		sourceFiles := strings.Fields(call(ctx, t, ctr, ref, "source-files"))
		require.Contains(t, sourceFiles, "dagger-module.toml")
		require.Contains(t, sourceFiles, "entrypoint/main.dang")
		require.NotContains(t, sourceFiles, "m/dagger-module.toml")
	}

	t.Run("local module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).WithDirectory(".", probeFixture(c))
		requireModuleContext(ctx, t, ctr, "./m")
	})

	t.Run("git module at a subpath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		gitDaemon, repoURL := gitService(ctx, t, c, probeFixture(c))
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)

		ctr := goGitBase(t, c).WithServiceBinding(gitHost, gitDaemon)
		requireModuleContext(ctx, t, ctr, repoURL+"#main:m")
	})

	// A directory source carries the whole directory it was given as its
	// context, so the source subpath is the only way to find the module in it.
	t.Run("directory module below the context root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		require.NoError(t, core.NewQuery(c).Directory().
			WithDirectory("pkgs", probeFixture(c)).
			AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "pkgs/m"}).
			AsModule().
			Serve(ctx))

		res, err := testutil.QueryWithClient[struct {
			M struct {
				SourceSubpath string
				Shared        string
			}
		}](c, t, `{m{sourceSubpath shared}}`, nil)
		require.NoError(t, err)
		require.Equal(t, "pkgs/m", res.M.SourceSubpath)
		require.Equal(t, "from the shared sibling", strings.TrimSpace(res.M.Shared))
	})
}

// The workspace an entrypoint receives has its working directory at the module
// it serves, so a shared entrypoint can find the module without a path
// generated into it.
func (ModuleSuite) TestModuleEntrypointWorkspaceCwdIsModuleDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory(
			".dagger/modules/tiny/entrypoint",
			core.NewQuery(c).Host().Directory("./testdata/modules/dang/module-entrypoint-cwd"),
		).
		With(daggerCallAt("tiny", "where")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, ".dagger/modules/tiny", strings.TrimPrefix(strings.TrimSpace(out), "/"))
}

// nonceEntrypointSource has the shape of an SDK entrypoint: its call keys the
// module process exec on the call digest, and the process returns a new value
// each time it runs. Failing raises after that exec, so dagql keeps no result
// for the call and evaluates it again on the next one.
var nonceEntrypointSource = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    let nonce = typeDef.withKind(TypeDefKind.STRING_KIND)
    [
      typeDef
        .withObject("Nonces")
        .withConstructor(function("", typeDef.withObject("Nonces")))
        .withFunction(function("Ttl", nonce).withCachePolicy(FunctionCachePolicy.DEFAULT, timeToLive: "3s"))
        .withFunction(function("Never", nonce).withCachePolicy(FunctionCachePolicy.NEVER))
        .withFunction(function("Session", nonce).withCachePolicy(FunctionCachePolicy.PER_SESSION))
        .withFunction(function("Failing", nonce)),
    ]
  }

  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
    callDigest: String,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      let result = container
        .from(%s)
        .withEnvVariable("DAGGER_MODULE_CALL_DIGEST", callDigest ?? "")
        .withExec(["sh", "-c", %s], stdin: fnName)
        .file("/result.json")
        .contents
      if (fnName == "Failing") {
        raise "failed after " + result
      }
      (result :: JSON!)
    }
  }
}
`, strconv.Quote(alpineImage), strconv.Quote(`printf '"%s"' "$(cat /proc/sys/kernel/random/uuid)" > /result.json`))

func nonceModule(c *dagger.Client, runID string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", `name = "nonces"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithNewFile("entrypoint/main.dang", nonceEntrypointSource).
		WithNewFile("run-id", runID)
}

func serveNonceModule(ctx context.Context, t *testctx.T, runID string) *dagger.Client {
	t.Helper()
	c := connect(ctx, t)
	require.NoError(t, nonceModule(c, runID).AsModuleSource().AsModule().Serve(ctx))
	return c
}

func queryNonce(t *testctx.T, c *dagger.Client, fn string) string {
	t.Helper()
	res, err := testutil.QueryWithClient[struct{ Nonces map[string]string }](c, t, "{nonces{"+fn+"}}", nil)
	require.NoError(t, err)
	return res.Nonces[fn]
}

// A function with an explicit TTL runs again whenever dagql evaluates it again,
// such as after its result expired. The exec its entrypoint keyed on the call
// digest must not answer for it with the old result.
func (ModuleSuite) TestModuleEntrypointCallAfterTTL(ctx context.Context, t *testctx.T) {
	// The TTL counts whole seconds.
	const expiry = 4 * time.Second
	requireRerunAfterTTL := func(t *testctx.T, c *dagger.Client) {
		t.Helper()
		first := queryNonce(t, c, "ttl")
		require.Equal(t, first, queryNonce(t, c, "ttl"), "a result within its TTL is cached")
		time.Sleep(expiry)
		require.NotEqual(t, first, queryNonce(t, c, "ttl"), "an expired result runs the function again")
	}

	t.Run("directory module", func(ctx context.Context, t *testctx.T) {
		requireRerunAfterTTL(t, serveNonceModule(ctx, t, rand.Text()))
	})

	t.Run("git module", func(ctx context.Context, t *testctx.T) {
		runID := rand.Text()
		serve := func(ctx context.Context, t *testctx.T) *dagger.Client {
			t.Helper()
			c := connect(ctx, t)
			gitDaemon, repoURL := gitService(ctx, t, c, nonceModule(c, runID))
			_, err := gitDaemon.Start(ctx)
			require.NoError(t, err)
			require.NoError(t, core.NewQuery(c).ModuleSource(repoURL+"#main").AsModule().Serve(ctx))
			return c
		}

		c := serve(ctx, t)
		first := queryNonce(t, c, "ttl")
		require.NoError(t, c.Close())
		time.Sleep(expiry)

		c = serve(ctx, t)
		assert.NotEqual(t, first, queryNonce(t, c, "ttl"), "an expired result from another session runs the function again")
		requireRerunAfterTTL(t, c)
	})

	// Short and long return the same container, so they share one cached
	// result, which expires at the earlier of their TTLs.
	t.Run("result shared with a shorter TTL", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		require.NoError(t, core.NewQuery(c).Directory().
			WithNewFile("dagger-module.toml", `name = "shared"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
			WithNewFile("entrypoint/main.dang", sharedContainerEntrypointSource).
			WithNewFile("run-id", rand.Text()).
			AsModuleSource().AsModule().Serve(ctx))
		call := func(fn string) {
			t.Helper()
			_, err := testutil.QueryWithClient[struct{}](c, t, "{shared{"+fn+"{id}}}", nil)
			require.NoError(t, err)
		}
		longRuns := func() string {
			t.Helper()
			res, err := testutil.QueryWithClient[struct{ Shared struct{ Runs string } }](c, t, `{shared{runs}}`, nil)
			require.NoError(t, err)
			return res.Shared.Runs
		}

		call("long")
		call("short")
		require.Equal(t, "1", longRuns())
		time.Sleep(2 * time.Second)
		call("long")
		require.Equal(t, "2", longRuns(), "long runs again once the shared result expired")
	})
}

// sharedContainerEntrypointSource returns one container from both Short and
// Long. The container is unique to the module, so no other call recreates it
// once it expired. Each run of a function appends its name to a cache volume,
// and Runs counts the runs of Long.
var sharedContainerEntrypointSource = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Shared")
        .withConstructor(function("", typeDef.withObject("Shared")))
        .withFunction(function("Short", typeDef.withObject("Container")).withCachePolicy(FunctionCachePolicy.DEFAULT, timeToLive: "1s"))
        .withFunction(function("Long", typeDef.withObject("Container")).withCachePolicy(FunctionCachePolicy.DEFAULT, timeToLive: "60s"))
        .withFunction(function("Runs", typeDef.withKind(TypeDefKind.STRING_KIND)).withCachePolicy(FunctionCachePolicy.NEVER)),
    ]
  }

  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
    callDigest: String,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      let runID = currentModule.source.file("run-id").contents
      let runs = container
        .from(%[1]s)
        .withMountedCache("/runs", cacheVolume("runs-" + runID))
        .withEnvVariable("DAGGER_MODULE_CALL_DIGEST", callDigest ?? "")
        .withExec(["sh", "-c", %[2]s], stdin: fnName)
        .stdout
      if (fnName == "Runs") {
        (runs :: JSON!)
      } else {
        if (runs == "") {
          raise "the module process wrote nothing"
        }
        (("\"" + toString(container.from(%[1]s).withEnvVariable("RUN_ID", runID).id) + "\"") :: JSON!)
      }
    }
  }
}
`, strconv.Quote(alpineImage), strconv.Quote(`fn=$(cat)
[ "$fn" = Runs ] || echo "$fn" >> /runs/log
printf '"%s"' "$(grep -cx Long /runs/log)"`))

// Without an explicit TTL, the exec of a call answers again whenever dagql
// evaluates the call again. The recipe digest it is keyed on already changes
// per call for never and per session for per session.
func (ModuleSuite) TestModuleEntrypointCallCachePolicies(ctx context.Context, t *testctx.T) {
	t.Run("never", func(ctx context.Context, t *testctx.T) {
		c := serveNonceModule(ctx, t, rand.Text())
		require.NotEqual(t, queryNonce(t, c, "never"), queryNonce(t, c, "never"))
	})

	t.Run("per session", func(ctx context.Context, t *testctx.T) {
		runID := rand.Text()
		c := serveNonceModule(ctx, t, runID)
		first := queryNonce(t, c, "session")
		require.Equal(t, first, queryNonce(t, c, "session"), "a result is cached within its session")
		require.NotEqual(t, first, queryNonce(t, serveNonceModule(ctx, t, runID), "session"), "another session runs the function again")
	})

	t.Run("default evaluated again", func(ctx context.Context, t *testctx.T) {
		c := serveNonceModule(ctx, t, rand.Text())
		failedAfter := regexp.MustCompile(`failed after \W*([0-9a-f-]{36})`)
		nonce := func() string {
			t.Helper()
			_, err := testutil.QueryWithClient[struct{ Nonces struct{ Failing string } }](c, t, `{nonces{failing}}`, nil)
			require.Error(t, err)
			match := failedAfter.FindStringSubmatch(err.Error())
			require.NotNil(t, match, err.Error())
			return match[1]
		}
		require.Equal(t, nonce(), nonce(), "the exec answers again")
	})
}

// tracedEntrypointSource has the shape of an SDK entrypoint: a build exec that
// emits no telemetry, then a module process that serves a dependency (here its
// own module, as a generated client of it would), calls the API under the
// injected TRACEPARENT, and writes the call result to a file.
var tracedEntrypointSource = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Traced")
        .withConstructor(function("", typeDef.withObject("Traced")))
        .withFunction(
          function("Body", typeDef.withKind(TypeDefKind.STRING_KIND))
            .withCachePolicy(FunctionCachePolicy.NEVER),
        ),
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
      let result = container
        .from(%s)
        .withEnvVariable("RUN_ID", workspace.file("/run-id").contents)
        .withExec(["sh", "-c", "echo built > /built"])
        .withExec(["sh", "-c", %s], stdin: fnName, experimentalPrivilegedNesting: true)
        .file("/result.json")
        .contents
      (result :: JSON!)
    }
  }
}
`, strconv.Quote(alpineImage), strconv.Quote(`set -e
auth=$(printf '%s:' "$DAGGER_SESSION_TOKEN" | base64)
query() {
  wget -q -O /dev/null \
    --header "Authorization: Basic $auth" \
    --header "traceparent: $TRACEPARENT" \
    --header "Content-Type: application/json" \
    --post-data "$1" \
    "http://127.0.0.1:$DAGGER_SESSION_PORT/query"
}
query '{"query":"{ serveModule(address: \"/.dagger/modules/traced\") }"}'
query '{"query":"{ directory { withNewFile(path: \"entrypoint-body\", contents: \"body\") { id } } }"}'
printf '"body"' > /result.json
`))

// The module process's API calls are the function body: they belong under the
// function call span, beside the entrypoint's plumbing rather than inside it.
// Serving a dependency is plumbing, so it belongs inside.
func (ModuleSuite) TestModuleEntrypointSpanTree(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.traced]
source = ".dagger/modules/traced"
`).
		// Body is never cached and its execs read this, so every run runs the module process.
		WithNewFile("run-id", rand.Text()).
		WithNewFile(".dagger/modules/traced/dagger-module.toml", `name = "traced"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithNewFile(".dagger/modules/traced/entrypoint/main.dang", tracedEntrypointSource).
		With(daggerCallAt("traced", "body")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "body", strings.TrimSpace(out))
	require.NoError(t, c.Close())

	const fnSpan, entrypointSpan = "Traced.body", "call module entrypoint"
	sink.read(func(db *dagui.DB) {
		var body, build, serve *dagui.Span
		var runtimeSpans int
		for span := range db.Spans.Iter() {
			switch {
			case span.Name == entrypointSpan:
				assert.True(t, span.Internal, "the entrypoint's plumbing is internal")
			case span.Name == "load sdk runtime":
				runtimeSpans++
			case span.Name == "Query.serveModule":
				serve = span
			case spanHasStringArg(span, "entrypoint-body"):
				body = span
			case spanHasStringArg(span, "echo built > /built"):
				build = span
			}
		}
		require.NotNil(t, body, "the module process's API call was not traced")
		require.NotNil(t, build, "the build exec was not traced")
		require.NotNil(t, serve, "the module process's serveModule call was not traced")
		// Independent properties: report every one that regressed.
		assert.Equal(t, fnSpan, nearestAncestor(body, fnSpan, entrypointSpan).Name, "the body belongs to the function call")
		assert.Equal(t, entrypointSpan, nearestAncestor(build, fnSpan, entrypointSpan).Name, "the build belongs to the entrypoint")
		assert.Equal(t, entrypointSpan, nearestAncestor(serve, fnSpan, entrypointSpan).Name, "serving a dependency belongs to the entrypoint")
		assert.Zero(t, runtimeSpans, "an entrypoint has no runtime to load")
	})
}

func nearestAncestor(span *dagui.Span, names ...string) *dagui.Span {
	for parent := span.ParentSpan; parent != nil; parent = parent.ParentSpan {
		for _, name := range names {
			if parent.Name == name {
				return parent
			}
		}
	}
	return &dagui.Span{}
}

func spanHasStringArg(span *dagui.Span, want string) bool {
	call := span.Call()
	if call == nil {
		return false
	}
	for _, arg := range call.Args {
		if arg.GetValue().GetString_() == want {
			return true
		}
		for _, value := range arg.GetValue().GetList().GetValues() {
			if value.GetString_() == want {
				return true
			}
		}
	}
	return false
}
