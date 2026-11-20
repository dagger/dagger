package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
)

// moduleServerEntrypoint serves its calls from the module-server fixture,
// whose results name the server instance and its call count. Its call member
// answers "exec", so a result tells which of the two ran the call.
var moduleServerEntrypoint = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    let text = typeDef.withKind(TypeDefKind.STRING_KIND)
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("Served", text).withCachePolicy(FunctionCachePolicy.Never))
        .withFunction(function("Module", text).withCachePolicy(FunctionCachePolicy.Never))
        .withFunction(function("Fail", text).withCachePolicy(FunctionCachePolicy.Never))
        .withFunction(function("Crash", text).withCachePolicy(FunctionCachePolicy.Never))
        .withFunction(function("Slow", text).withCachePolicy(FunctionCachePolicy.Never)),
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
      (JSON.encode("exec") :: JSON!)
    }
  }

  pub serve: Container! {
    container
      .from(%s)
      .withFile("/module-server", currentModule.source.file("module-server"))
      .withDefaultArgs(["/module-server"])
  }
}
`, strconv.Quote(alpineImage))

func moduleServerModule(c *dagger.Client) *core.Directory {
	server := core.NewQuery(c).Container().
		From(golangImage).
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedFile("/src/main.go", core.NewQuery(c).Host().File("testdata/module-server/main.go")).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/module-server", "main.go"}).
		File("/module-server")
	return core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "probe"`, 1)).
		WithNewFile("entrypoint/main.dang", moduleServerEntrypoint).
		WithFile("module-server", server)
}

func serveModuleServerModule(ctx context.Context, t *testctx.T) *dagger.Client {
	t.Helper()
	c := connect(ctx, t)
	require.NoError(t, moduleServerModule(c).AsModuleSource().AsModule().Serve(ctx))
	return c
}

func queryProbe(t *testctx.T, c *dagger.Client, fn string) (string, error) {
	t.Helper()
	res, err := testutil.QueryWithClient[struct{ Probe map[string]string }](c, t, "{probe{"+fn+"}}", nil)
	if err != nil {
		return "", err
	}
	return res.Probe[fn], nil
}

// servedBy returns the server instance that served a call of Served.
func servedBy(t *testctx.T, c *dagger.Client) string {
	t.Helper()
	out, err := queryProbe(t, c, "served")
	require.NoError(t, err)
	instance, _, ok := strings.Cut(out, ":")
	require.True(t, ok, "the call member ran the call: %s", out)
	return instance
}

func (ModuleSuite) TestModuleEntrypointServe(ctx context.Context, t *testctx.T) {
	t.Run("the session's calls share one server", func(ctx context.Context, t *testctx.T) {
		c := serveModuleServerModule(ctx, t)
		first, err := queryProbe(t, c, "served")
		require.NoError(t, err)
		second, err := queryProbe(t, c, "served")
		require.NoError(t, err)
		require.NotEqual(t, first, second)
		require.Equal(t, strings.Split(first, ":")[0], strings.Split(second, ":")[0])
	})

	t.Run("each call's client serves the module", func(ctx context.Context, t *testctx.T) {
		out, err := queryProbe(t, serveModuleServerModule(ctx, t), "module")
		require.NoError(t, err)
		require.Contains(t, out, `"name":"probe"`)
	})

	t.Run("a failed call fails and keeps the server", func(ctx context.Context, t *testctx.T) {
		c := serveModuleServerModule(ctx, t)
		before := servedBy(t, c)
		_, err := queryProbe(t, c, "fail")
		require.ErrorContains(t, err, "boom")
		require.Equal(t, before, servedBy(t, c))
	})

	t.Run("a server that dies fails its call and the next call starts another", func(ctx context.Context, t *testctx.T) {
		c := serveModuleServerModule(ctx, t)
		before := servedBy(t, c)
		_, err := queryProbe(t, c, "crash")
		require.ErrorContains(t, err, "exited during the call")
		require.NotEqual(t, before, servedBy(t, c))
	})

	t.Run("a cancelled call kills the server and the next call starts another", func(ctx context.Context, t *testctx.T) {
		c := serveModuleServerModule(ctx, t)
		before := servedBy(t, c)
		cancelCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		require.Error(t, c.Do(cancelCtx, &dagger.Request{Query: "{probe{slow}}"}, &dagger.Response{}))
		// A server that was not killed stays busy with Slow for 5s, and a
		// call that finds it busy runs the call member, so within 3s only a
		// kill lets a call reach a server, and a new one.
		var out string
		require.Eventually(t, func() bool {
			var err error
			out, err = queryProbe(t, c, "served")
			return err == nil && out != "exec"
		}, 3*time.Second, 50*time.Millisecond, "the cancelled call left the server busy")
		instance, _, _ := strings.Cut(out, ":")
		require.NotEqual(t, before, instance, "the cancelled call's server still serves")
	})

	// The server starts under the dependent module's client of the first
	// call, which closes before the second call reaches the server.
	t.Run("a dependency's calls share one server", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		mod := core.NewQuery(c).Directory().
			WithDirectory("probe", moduleServerModule(c)).
			WithNewFile("caller/dagger-module.toml", `name = "caller"

[[dependencies]]
name = "probe"
source = "../probe"

[runtime]
source = "dang"
`).
			WithNewFile("caller/main.dang", `type Caller {
  pub first: String! {
    probe.served
  }

  pub second: String! {
    probe.served
  }
}
`)
		require.NoError(t, mod.AsModuleSource(core.DirectoryAsModuleSourceOpts{SourceRootPath: "caller"}).AsModule().Serve(ctx))
		call := func(fn string) string {
			t.Helper()
			res, err := testutil.QueryWithClient[struct{ Caller map[string]string }](c, t, "{caller{"+fn+"}}", nil)
			require.NoError(t, err)
			instance, _, ok := strings.Cut(res.Caller[fn], ":")
			require.True(t, ok, "the call member ran the call: %s", res.Caller[fn])
			return instance
		}
		require.Equal(t, call("first"), call("second"))
	})

	t.Run("a call that finds the server busy runs the call member", func(ctx context.Context, t *testctx.T) {
		c := serveModuleServerModule(ctx, t)
		instance := servedBy(t, c)
		slow := make(chan string, 1)
		go func() {
			out, err := queryProbe(t, c, "slow")
			if err != nil {
				out = err.Error()
			}
			slow <- out
		}()
		// Slow holds the server for 5s. A poll that the server takes just
		// before Slow arrives holds it instead, and Slow runs the call member.
		var slowOut string
		require.Eventually(t, func() bool {
			select {
			case slowOut = <-slow:
				return slowOut == "exec"
			default:
			}
			out, err := queryProbe(t, c, "served")
			return err == nil && out == "exec"
		}, 4*time.Second, 50*time.Millisecond)
		if slowOut == "" {
			slowOut = <-slow
			require.True(t, strings.HasPrefix(slowOut, instance+":"), slowOut)
		}
	})
}
