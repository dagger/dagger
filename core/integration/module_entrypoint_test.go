package core

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/buildkit/identity"
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
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
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
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		WithNewFile("sub/dir/.keep", "").
		WithWorkdir("sub/dir").
		With(daggerCallAt("tiny", "hello"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
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
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
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
	served := c.Directory().WithDirectory(
		"entrypoint",
		c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
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
	served := c.Directory().
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
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
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
			c.Host().Directory("./testdata/modules/dang/module-entrypoint-cwd"),
		).
		With(daggerCallAt("tiny", "where")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, ".dagger/modules/tiny", strings.TrimPrefix(strings.TrimSpace(out), "/"))
}

// tracedEntrypointSource has the shape of an SDK entrypoint: a build exec that
// emits no telemetry, then a module process that calls the API under the
// injected TRACEPARENT and writes the call result to a file.
var tracedEntrypointSource = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Traced")
        .withConstructor(function("", typeDef.withObject("Traced")))
        .withFunction(
          function("Body", typeDef.withKind(TypeDefKind.STRING_KIND))
            .withCachePolicy(FunctionCachePolicy.Never),
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
query='{"query":"{ directory { withNewFile(path: \"entrypoint-body\", contents: \"body\") { id } } }"}'
wget -q -O /dev/null \
  --header "Authorization: Basic $auth" \
  --header "traceparent: $TRACEPARENT" \
  --header "Content-Type: application/json" \
  --post-data "$query" \
  "http://127.0.0.1:$DAGGER_SESSION_PORT/query"
printf '"body"' > /result.json
`))

// The module process's API calls are the function body: they belong under the
// function call span, beside the entrypoint's plumbing rather than inside it.
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
		WithNewFile("run-id", identity.NewID()).
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
		var body, build *dagui.Span
		var runtimeSpans int
		for span := range db.Spans.Iter() {
			switch {
			case span.Name == entrypointSpan:
				assert.True(t, span.Internal, "the entrypoint's plumbing is internal")
			case span.Name == "load sdk runtime":
				runtimeSpans++
			case spanHasStringArg(span, "entrypoint-body"):
				body = span
			case spanHasStringArg(span, "echo built > /built"):
				build = span
			}
		}
		require.NotNil(t, body, "the module process's API call was not traced")
		require.NotNil(t, build, "the build exec was not traced")
		// Independent properties: report every one that regressed.
		assert.Equal(t, fnSpan, nearestAncestor(body, fnSpan, entrypointSpan).Name, "the body belongs to the function call")
		assert.Equal(t, entrypointSpan, nearestAncestor(build, fnSpan, entrypointSpan).Name, "the build belongs to the entrypoint")
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
