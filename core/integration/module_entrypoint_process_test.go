package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"dagger.io/dagger/core"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// The process reads the engine with the dagger CLI that the module ships, so
// the test needs no SDK. Lazy separates creating the process from running it.
// Shared starts a process whose recipe holds nothing of the module, so modules
// sharing its cache key would show.
const moduleProcessEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("Process", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Grandchild", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Call", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Lazy", typeDef.withObject("Container")))
        .withFunction(function("Shared", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("DefaultNesting", typeDef.withKind(TypeDefKind.STRING_KIND))),
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
    } else if (fnName == "Process") {
      (JSON.encode(process("process").stdout) :: JSON!)
    } else if (fnName == "Grandchild") {
      (JSON.encode(process("grandchild").stdout) :: JSON!)
    } else if (fnName == "Call") {
      (JSON.encode(process("call").stdout) :: JSON!)
    } else if (fnName == "DefaultNesting") {
      (JSON.encode(sharedProcess.withExec(["sh", "/query.sh"]).stdout) :: JSON!)
    } else if (fnName == "Lazy") {
      (JSON.encode(process("process").id) :: JSON!)
    } else {
      (JSON.encode(shared) :: JSON!)
    }
  }

  let shared: String! {
    sharedProcess.withExec(["sh", "/query.sh"], experimentalPrivilegedNesting: true).stdout
  }

  let sharedProcess: Container! {
    container.from("%[1]s").withNewFile("/query.sh", %[2]q)
  }

  let process(mode: String!): Container! {
    container
      .from("%[1]s")
      .withFile("/usr/local/bin/dagger", currentModule.source.file("bin/dagger"))
      .withFile("/probe.sh", currentModule.source.file("probe.sh"))
      .withEnvVariable("IMAGE", "%[1]s")
      .withExec(["sh", "/probe.sh", mode], experimentalPrivilegedNesting: true)
  }
}
`

// The grandchild reuses the CLI through its file ID, which only a process that
// serves the module can read.
const moduleProcessScript = `set -e
query() { echo "$1" | dagger query; }
case "$1" in
process)
  query '{currentModule{name}}'
  ;;
call)
  query '{currentFunctionCall{name}}' 2>&1 || true
  ;;
grandchild)
  cli=$(query '{currentModule{source{file(path:"bin/dagger"){id}}}}' | tr -d ' \n' | sed 's/.*"id":"\([^"]*\)".*/\1/')
  query "{
    container {
      from(address: \"$IMAGE\") {
        withFile(path: \"/usr/local/bin/dagger\", source: \"$cli\") {
          withExec(
            args: [\"sh\", \"-c\", \"echo {currentModule{name}} | dagger query\"]
            experimentalPrivilegedNesting: true
            expect: ANY
          ) { stderr }
        }
      }
    }
  }"
  ;;
esac
`

// sharedProcessScript asks the engine for the module without the CLI, which
// would put a file of the module into the process's recipe.
const sharedProcessScript = `wget -q -O- \
  --header "Content-Type: application/json" \
  --header "Authorization: Basic $(printf %s: "$DAGGER_SESSION_TOKEN" | base64)" \
  --post-data '{"query":"{currentModule{name}}"}' \
  "http://127.0.0.1:$DAGGER_SESSION_PORT/query"
`

func moduleProcessFixture(t *testctx.T, ctr *core.Container, dir, name string) *core.Container {
	t.Helper()
	return ctr.
		WithNewFile(dir+"/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, fmt.Sprintf("name = %q", name), 1)).
		WithNewFile(dir+"/entrypoint/main.dang", fmt.Sprintf(moduleProcessEntrypoint, alpineImage, sharedProcessScript)).
		WithNewFile(dir+"/probe.sh", moduleProcessScript)
}

// An SDK entrypoint runs each function in a process that it starts, so that
// process must serve the module, as the process of a container runtime does.
func (ModuleSuite) TestModuleEntrypointProcessServesModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cli := daggerCliFile(t, c)
	ctr := goGitBase(t, c)
	ctr = moduleProcessFixture(t, ctr, "m", "m").WithFile("m/bin/dagger", cli)
	call := func(ctx context.Context, t *testctx.T, ctr *core.Container, args ...string) string {
		t.Helper()
		out, err := ctr.With(daggerCallAt(args[0], args[1:]...)).Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	t.Run("the process serves the module", func(ctx context.Context, t *testctx.T) {
		require.Contains(t, call(ctx, t, ctr, "./m", "process"), `"name": "m"`)
	})

	t.Run("a process with the default nesting serves the module", func(ctx context.Context, t *testctx.T) {
		require.Contains(t, call(ctx, t, ctr, "./m", "default-nesting"), `"name":"m"`)
	})

	t.Run("the process does not get the call", func(ctx context.Context, t *testctx.T) {
		require.Contains(t, call(ctx, t, ctr, "./m", "call"), "no current function call")
	})

	t.Run("a process started by the module process does not", func(ctx context.Context, t *testctx.T) {
		require.Contains(t, call(ctx, t, ctr, "./m", "grandchild"), "no current module")
	})

	t.Run("the caller that runs the returned process gets the module", func(ctx context.Context, t *testctx.T) {
		require.Contains(t, call(ctx, t, ctr, "./m", "lazy", "stdout"), `"name": "m"`)
	})

	t.Run("modules with the same process do not share its result", func(ctx context.Context, t *testctx.T) {
		both := moduleProcessFixture(t, ctr, "n", "n").WithFile("n/bin/dagger", cli)
		require.Contains(t, call(ctx, t, both, "./m", "shared"), `"name":"m"`)
		require.Contains(t, call(ctx, t, both, "./n", "shared"), `"name":"n"`)
	})
}

func (ModuleSuite) TestModuleFunctionProcessHasNoModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	out, err := goGitBase(t, c).
		WithNewFile("dagger.json", `{"name":"plain","engineVersion":"latest","sdk":{"source":"dang"}}`).
		WithNewFile("main.dang", fmt.Sprintf(`type Plain {
  pub process(cli: File!): String! {
    container
      .from("%s")
      .withFile("/usr/local/bin/dagger", cli)
      .withExec(["sh", "-c", "echo '{currentModule{name}}' | dagger query"], experimentalPrivilegedNesting: true, expect: ReturnType.ANY)
      .stderr
  }
}
`, alpineImage)).
		With(daggerCall("process", "--cli", testCLIBinPath)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "no current module")
}

// moduleCacheEntrypoint appends the module's name to a cache volume, once from
// the entrypoint itself and once from a process it starts, and returns every
// name appended so far, which shows the modules each volume is shared with.
const moduleCacheEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("Entrypoint", typeDef.withKind(TypeDefKind.STRING_KIND)))
        .withFunction(function("Process", typeDef.withKind(TypeDefKind.STRING_KIND))),
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
    } else if (fnName == "Entrypoint") {
      (JSON.encode(base
        .withMountedCache("/cache", cacheVolume(%[2]q))
        .withExec(["sh", "-c", "echo " + currentModule.name + " >> /cache/log; cat /cache/log"])
        .stdout) :: JSON!)
    } else {
      (JSON.encode(base
        .withFile("/usr/local/bin/dagger", currentModule.source.file("bin/dagger"))
        .withFile("/probe.sh", currentModule.source.file("probe.sh"))
        .withEnvVariable("IMAGE", %[1]q)
        .withEnvVariable("NAME", currentModule.name)
        .withExec(["sh", "/probe.sh"], experimentalPrivilegedNesting: true)
        .stdout) :: JSON!)
    }
  }

  let base: Container! {
    container.from(%[1]q)
  }
}
`

// moduleCacheProcessScript does what the entrypoint does, from the module's
// process.
const moduleCacheProcessScript = `set -e
query() { echo "$1" | dagger query; }
cache=$(query '{cacheVolume(key: "%[1]s"){id}}' | tr -d ' \n' | sed 's/.*"id":"\([^"]*\)".*/\1/')
query "{
  container {
    from(address: \"$IMAGE\") {
      withMountedCache(path: \"/cache\", cache: \"$cache\") {
        withExec(args: [\"sh\", \"-c\", \"echo $NAME >> /cache/log; cat /cache/log\"]) { stdout }
      }
    }
  }
}" | tr -d ' \n' | sed 's/.*"stdout":"\([^"]*\)".*/\1/'
`

// An SDK entrypoint builds every module with the same SDK code, such as the Go
// SDK building its dispatcher, so its cache volumes are shared across modules.
// The module's own code keeps its volumes.
func (ModuleSuite) TestModuleEntrypointCacheVolumes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cli := daggerCliFile(t, c)
	entrypointKey := "entrypoint-" + rand.Text()
	processKey := "process-" + rand.Text()
	ctr := goGitBase(t, c)
	for _, name := range []string{"m", "n"} {
		ctr = ctr.
			WithNewFile(name+"/dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, fmt.Sprintf("name = %q", name), 1)).
			WithNewFile(name+"/entrypoint/main.dang", fmt.Sprintf(moduleCacheEntrypoint, alpineImage, entrypointKey)).
			WithNewFile(name+"/probe.sh", fmt.Sprintf(moduleCacheProcessScript, processKey)).
			WithFile(name+"/bin/dagger", cli)
	}
	call := func(ctx context.Context, t *testctx.T, args ...string) string {
		t.Helper()
		out, err := ctr.With(daggerCallAt(args[0], args[1:]...)).Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	t.Run("the entrypoint's volumes are shared across modules", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, "m\n", call(ctx, t, "./m", "entrypoint"))
		require.Equal(t, "m\nn\n", call(ctx, t, "./n", "entrypoint"))
	})

	t.Run("the module process's volumes are not", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, `m\n`, strings.TrimSpace(call(ctx, t, "./m", "process")))
		require.Equal(t, `n\n`, strings.TrimSpace(call(ctx, t, "./n", "process")))
	})
}
