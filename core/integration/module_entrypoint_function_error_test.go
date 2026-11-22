package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger/core"

	dagcore "github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// functionErrorEntrypoint marks its module process with callDigest, as
// generated entrypoints do, and runs each function in it, raising no error of
// its own: whatever the call fails with comes from the engine.
const functionErrorEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    let str = typeDef.withKind(TypeDefKind.STRING_KIND)
    [
      typeDef
        .withObject("Probe")
        .withConstructor(function("", typeDef.withObject("Probe")))
        .withFunction(function("Env", str))
        .withFunction(function("Native", str))
        .withFunction(function("Exit", str))
        .withFunction(function("Inner", str))
        .withFunction(function("Stale", str))
        .withFunction(function("Fifo", str))
        .withFunction(function("ExecError", str))
        .withFunction(function("Data", str))
        .withFunction(function("ById", str))
        .withFunction(function("Huge", str))
        .withFunction(function("Root", str)),
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
    } else if (fnName == "Env") {
      let build = base.withExec(["sh", "/probe.sh", "env"]).stdout
      let process = base
        .withEnvVariable("%[2]s", callDigest ?? "")
        .withExec(["sh", "/probe.sh", "env"])
        .stdout
      (JSON.encode("build: " + build + "process: " + process) :: JSON!)
    } else if (fnName == "Root") {
      (JSON.encode(base
        .withEnvVariable("%[2]s", callDigest ?? "")
        .withExec(["true"])
        .directory("/")
        .entries
        .join(",")) :: JSON!)
    } else {
      (base
        .withEnvVariable("%[2]s", callDigest ?? "")
        .withExec(["sh", "/probe.sh", fnName], experimentalPrivilegedNesting: true)
        .file("/result.json")
        .contents :: JSON!)
    }
  }

  let base: Container! {
    container
      .from("%[1]s")
      .withFile("/probe.sh", currentModule.source.file("probe.sh"))
      .withFile("/nonce", currentModule.source.file("nonce"))
  }
}
`

// functionErrorScript is the module process: Native fails with an error of
// its own, Exit dies without one, Inner fails with an API error, whose
// message names the failed exec, and Stale succeeds after writing an error.
// ExecError, Data and ById report their errors in the structured forms, and
// Huge in a file over the limit.
const functionErrorScript = `query() {
  wget -q -O- \
    --header "Content-Type: application/json" \
    --header "Authorization: Basic $(printf %%s: "$DAGGER_SESSION_TOKEN" | base64)" \
    --header "traceparent: $TRACEPARENT" \
    --post-data "{\"query\":\"$1\"}" \
    "http://127.0.0.1:$DAGGER_SESSION_PORT/query"
}
case "$1" in
env)
  if [ -n "$%[1]s" ]; then echo set; else echo unset; fi
  ;;
Native)
  echo "native log $(cat /nonce)"
  printf 'the function failed %%s' "$(cat /nonce)" > "$%[1]s"
  exit 1
  ;;
Exit)
  exit 3
  ;;
Inner)
  out=$(wget -q -O- \
    --header "Content-Type: application/json" \
    --header "Authorization: Basic $(printf %%s: "$DAGGER_SESSION_TOKEN" | base64)" \
    --header "traceparent: $TRACEPARENT" \
    --post-data '{"query":"{container{from(address:\"%[2]s\"){withExec(args:[\"sh\",\"-c\",\"echo inner $(cat /nonce); exit 7\"]){stdout}}}}"}' \
    "http://127.0.0.1:$DAGGER_SESSION_PORT/query")
  printf '%%s' "$out" | sed 's/.*"message":"\([^"]*\)".*/\1/' > "$%[1]s"
  exit 1
  ;;
Fifo)
  mkfifo "$%[1]s"
  exit 4
  ;;
ExecError)
  out=$(query '{container{from(address:\"%[2]s\"){withExec(args:[\"sh\",\"-c\",\"echo inner-out; echo inner-err >&2; exit 7\"]){stdout}}}}')
  # The API error, its extensions as the error's values.
  printf '%%s' "$out" | sed 's/^{"errors":\[\(.*\)\],"data":null}$/\1/; s/"extensions":/"values":/' > "$%[1]s"
  exit 1
  ;;
Data)
  printf '{"message": "custom failure", "values": {"answer": 42, "nested": {"a": [1, 2]}}}' > "$%[1]s"
  exit 1
  ;;
ById)
  out=$(query '{error(message:\"by id\"){withValue(name:\"answer\",value:\"42\"){id}}}')
  printf '{"id": "%%s"}' "$(printf '%%s' "$out" | sed 's/.*"id":"\([^"]*\)".*/\1/')" > "$%[1]s"
  exit 1
  ;;
Huge)
  head -c %[3]d /dev/zero | tr '\0' x > "$%[1]s"
  exit 5
  ;;
Stale)
  echo "stale error" > "$%[1]s"
  printf '"%%s"' "$(cat /proc/sys/kernel/random/uuid)" > /result.json
  ;;
esac
`

func functionErrorFixture(nonce string) func(*core.Container) *core.Container {
	return func(ctr *core.Container) *core.Container {
		return ctr.
			WithNewFile("m/dagger-module.toml", sourcesModuleManifest).
			WithNewFile("m/entrypoint/main.dang", fmt.Sprintf(functionErrorEntrypoint, alpineImage, dagcore.ModuleCallDigestEnv)).
			WithNewFile("m/probe.sh", fmt.Sprintf(functionErrorScript, engineutil.FunctionErrorPathEnv, alpineImage, engineutil.MaxFunctionErrorBytes+1)).
			WithNewFile("m/nonce", nonce)
	}
}

// A module process reports its function's error in the file the engine
// gives it, and the call fails with that error as the function's own.
func (ModuleSuite) TestModuleEntrypointFunctionErrorFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	nonce := rand.Text()
	ctr := goGitBase(t, c).With(functionErrorFixture(nonce))
	call := func(fn string) (string, error) {
		return ctr.With(daggerCallAt("./m", fn)).Stdout(ctx)
	}

	t.Run("only the module process gets the file", func(ctx context.Context, t *testctx.T) {
		out, err := call("env")
		require.NoError(t, err)
		require.Equal(t, "build: unset\nprocess: set", strings.TrimSpace(out))
	})

	t.Run("the file stays out of the process's filesystem", func(ctx context.Context, t *testctx.T) {
		out, err := call("root")
		require.NoError(t, err)
		require.NotContains(t, out, ".dagger_function_error")
		require.Contains(t, out, "etc/")
	})

	// The errors the CLI prints for the call, and its exit code.
	callErr := func(t *testctx.T, fn string) ([]string, int) {
		t.Helper()
		_, err := call(fn)
		var execErr *core.ExecError
		require.ErrorAs(t, err, &execErr)
		require.NotContains(t, execErr.Stderr, "call module entrypoint")
		var errs []string
		for _, line := range strings.Split(ansiEscapes.ReplaceAllString(execErr.Stderr, ""), "\n") {
			if msg, ok := strings.CutPrefix(line, "! "); ok {
				errs = append(errs, strings.TrimSpace(msg))
			}
		}
		require.NotEmpty(t, errs, execErr.Stderr)
		return errs, execErr.ExitCode
	}

	t.Run("the call fails with the error the process wrote", func(ctx context.Context, t *testctx.T) {
		errs, _ := callErr(t, "native")
		require.Equal(t, []string{"the function failed " + nonce}, errs)
	})

	t.Run("a process that wrote none fails the call with its exit code", func(ctx context.Context, t *testctx.T) {
		errs, code := callErr(t, "exit")
		require.Equal(t, []string{"exit code: 3"}, errs)
		require.Equal(t, 3, code, "the CLI exits with the process's code")
	})

	t.Run("only a regular file is read", func(ctx context.Context, t *testctx.T) {
		// A named pipe would block the read forever.
		errs, code := callErr(t, "fifo")
		require.Equal(t, []string{"exit code: 4"}, errs)
		require.Equal(t, 4, code)
	})

	t.Run("a file over the limit is no error", func(ctx context.Context, t *testctx.T) {
		errs, code := callErr(t, "huge")
		require.Equal(t, []string{"exit code: 5"}, errs)
		require.Equal(t, 5, code)
	})

	t.Run("a text the process wrote is the message", func(ctx context.Context, t *testctx.T) {
		errs, _ := callErr(t, "inner")
		require.Equal(t, []string{"exit code: 7"}, errs)
	})

	t.Run("an API error the process wrote exits with its code", func(ctx context.Context, t *testctx.T) {
		errs, code := callErr(t, "exec-error")
		require.Equal(t, []string{"exit code: 7"}, errs)
		require.Equal(t, 7, code, "as the inner exec did")
	})

	t.Run("a success ignores an error the process wrote", func(ctx context.Context, t *testctx.T) {
		first, err := call("stale")
		require.NoError(t, err)
		require.NotEmpty(t, strings.TrimSpace(first))
		again, err := call("stale")
		require.NoError(t, err)
		require.Equal(t, first, again, "the success is cached, file and all")
	})
}

// A module calling another sees the error the other's module process
// reported, values and all, as it would one a runtime's process returned: an
// ExecError for an exec's.
func (ModuleSuite) TestModuleEntrypointFunctionErrorValues(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", strings.Replace(sourcesModuleManifest, `name = "m"`, `name = "probe"`, 1)).
		WithNewFile("entrypoint/main.dang", fmt.Sprintf(functionErrorEntrypoint, alpineImage, dagcore.ModuleCallDigestEnv)).
		WithNewFile("probe.sh", fmt.Sprintf(functionErrorScript, engineutil.FunctionErrorPathEnv, alpineImage, engineutil.MaxFunctionErrorBytes+1)).
		WithNewFile("nonce", rand.Text())
	require.NoError(t, mod.AsModuleSource().AsModule().Serve(ctx))
	callErr := func(t *testctx.T, fn string) (error, map[string]any) {
		t.Helper()
		_, err := testutil.QueryWithClient[struct{ Probe struct{} }](c, t, "{probe{"+fn+"}}", nil)
		require.Error(t, err)
		var extended interface{ Extensions() map[string]any }
		require.ErrorAs(t, err, &extended)
		return err, extended.Extensions()
	}

	t.Run("an exec's error is an ExecError", func(ctx context.Context, t *testctx.T) {
		err, _ := callErr(t, "execError")
		var execErr *core.ExecError
		require.ErrorAs(t, err, &execErr)
		require.Equal(t, 7, execErr.ExitCode)
		require.Equal(t, "inner-out", execErr.Stdout)
		require.Equal(t, "inner-err", execErr.Stderr)
		require.Contains(t, err.Error(), "exit code: 7")
		require.NotContains(t, err.Error(), "call module entrypoint")
	})

	t.Run("the values the process reported", func(ctx context.Context, t *testctx.T) {
		err, ext := callErr(t, "data")
		require.Contains(t, err.Error(), "custom failure")
		require.EqualValues(t, 42, ext["answer"])
		require.Equal(t, map[string]any{"a": []any{float64(1), float64(2)}}, ext["nested"])
		require.NotContains(t, ext, dagcore.FunctionErrorExtension)
		require.NotContains(t, ext, dagcore.ModuleProcessExtension)
	})

	t.Run("an Error the process reported by ID", func(ctx context.Context, t *testctx.T) {
		err, ext := callErr(t, "byId")
		require.Contains(t, err.Error(), "by id")
		require.EqualValues(t, 42, ext["answer"])
	})

	t.Run("a text the process reported is the message", func(ctx context.Context, t *testctx.T) {
		err, ext := callErr(t, "native")
		require.Contains(t, err.Error(), "the function failed")
		require.NotContains(t, ext, "_type", "no values but its origin: %v", ext)
	})

	t.Run("a process that reported none", func(ctx context.Context, t *testctx.T) {
		err, _ := callErr(t, "huge")
		var execErr *core.ExecError
		require.ErrorAs(t, err, &execErr)
		require.Equal(t, 5, execErr.ExitCode)
	})
}

// The error of a module process names where it comes from: the function
// call, for an error of the function's own or a process that died, and the
// failed call an API error names.
func (ModuleSuite) TestModuleEntrypointFunctionErrorOrigin(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)
	nonce := rand.Text()
	ctr := goGitBase(t, c).With(functionErrorFixture(nonce))
	for _, fn := range []string{"native", "exit", "inner", "exec-error", "data"} {
		_, err := ctr.
			WithExec([]string{"dagger", "--progress=logs", "call", "-m", "./m", fn}, core.ContainerWithExecOpts{
				UseEntrypoint: true,
				Expect:        core.ReturnTypeFailure,
			}).
			Sync(ctx)
		require.NoError(t, err)
	}
	require.NoError(t, c.Close())

	sink.read(func(db *dagui.DB) {
		// The origins of the function call's span; one naming the span
		// itself is not recorded.
		origins := func(fnSpan string) []*dagui.Span {
			t.Helper()
			for span := range db.Spans.Iter() {
				if span.Name == fnSpan && span.IsFailed() {
					return span.ErrorOrigins.Spans()
				}
			}
			require.Fail(t, "no failed span "+fnSpan)
			return nil
		}
		// Module m namespaces its Probe object.
		require.Empty(t, origins("MProbe.native"), "the function call is the origin")
		require.Empty(t, origins("MProbe.exit"), "the function call is the origin")
		inner := origins("MProbe.inner")
		require.Len(t, inner, 1)
		require.True(t, spanHasStringArg(inner[0], "echo inner $(cat /nonce); exit 7"),
			"the API error's origin is its exec: %s", inner[0].Name)
		reported := origins("MProbe.execError")
		require.Len(t, reported, 1)
		require.True(t, spanHasStringArg(reported[0], "echo inner-out; echo inner-err >&2; exit 7"),
			"a reported API error's origin is its exec: %s", reported[0].Name)
		require.Empty(t, origins("MProbe.data"), "the function call is the origin")
	})
}
