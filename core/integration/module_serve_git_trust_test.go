package core

// These tests cover Query.serveModule on a private git address from a
// module's code: the module's own config must declare the client for the
// session's git credentials to be used, as a module's config declares a
// dependency.

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const serveGitTrustToken = "serve-git-trust-token"

// serveGitTrustCallerSource adds to the tree tests' caller a function that
// returns the error serving an address gives the module's code.
const serveGitTrustCallerSource = serveTreeCallerSource + `
func (m *Caller) Attempt(ctx context.Context, address string) string {
	if err := dag.ServeModule(ctx, address); err != nil {
		return err.Error()
	}
	return "served"
}
`

// serveGitTrustNonceSource adds to the caller a function whose result is new
// on every run, so an unchanged result proves a cache hit.
const serveGitTrustNonceSource = `package main

import (
	"context"
	"strconv"
	"time"
)

func (m *Caller) Nonce(ctx context.Context, address string) string {
	if err := dag.ServeModule(ctx, address); err != nil {
		return err.Error()
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}
`

// serveGitTrustSessionSource adds to the caller functions whose results are
// new on every run: one that serves nothing, and one that serves an address
// and returns a directory it builds, which the engine holds as its own
// result.
const serveGitTrustSessionSource = `package main

import (
	"context"
	"strconv"
	"time"

	"dagger/caller/internal/dagger"
)

func (m *Caller) Plain() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

func (m *Caller) NonceDir(ctx context.Context, address string) *dagger.Directory {
	if err := dag.ServeModule(ctx, address); err != nil {
		return dag.Directory().WithNewFile("nonce", err.Error())
	}
	return dag.Directory().WithNewFile("nonce", strconv.FormatInt(time.Now().UnixNano(), 10))
}
`

// serveGitTrustRelaySource adds to the caller functions that serve the
// helper module from the caller's tree and return what its nonce function
// returns, a string with no dependency on how it was computed.
const serveGitTrustRelaySource = `package main

import (
	"context"
	"strconv"
	"time"
)

func (m *Caller) Relay(ctx context.Context, address string) (string, error) {
	return relay(ctx, address)
}

func (m *Caller) RelayAgain(ctx context.Context, address string) (string, error) {
	return relay(ctx, address)
}

func relay(ctx context.Context, address string) (string, error) {
	if err := dag.ServeModule(ctx, "/modules/helper"); err != nil {
		return "", err
	}
	var out string
	q := dag.QueryBuilder().Select("helper").Select("nonce").Arg("address", address).Bind(&out)
	return out, q.Execute(ctx)
}

// Touched serves the helper, whose touch function serves address and
// returns nothing, and returns a new nonce once it succeeded.
func (m *Caller) Touched(ctx context.Context, address string) (string, error) {
	if err := dag.ServeModule(ctx, "/modules/helper"); err != nil {
		return "", err
	}
	if err := dag.QueryBuilder().Select("helper").Select("touch").Arg("address", address).Execute(ctx); err != nil {
		return err.Error(), nil
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10), nil
}
`

const serveGitTrustHelperManifest = `{"name":"helper","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`

// serveGitTrustHelperSource is a helper module whose nonce function serves
// an address, as the caller's does, and whose touch function serves an
// address and returns nothing.
var serveGitTrustHelperSource = strings.ReplaceAll(serveGitTrustNonceSource, "*Caller", "*Helper") + `
type Helper struct{}

func (m *Helper) Touch(ctx context.Context, address string) error {
	return dag.ServeModule(ctx, address)
}
`

// serveGitTrustRelayConfig declares client for the caller and for the
// helper the caller serves from its tree.
func serveGitTrustRelayConfig(client string) string {
	return fmt.Sprintf(`[modules.caller]
source = "modules/caller"

[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes."modules/caller"]
is-module = true
clients = [%q, "./modules/helper"]

[sdks.go.scopes."modules/helper"]
is-module = true
clients = [%q]
`, client, client)
}

// serveGitTrustEntrypointManifest is a module that runs its calls through
// its own entrypoint.
const serveGitTrustEntrypointManifest = `name = "epcaller"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`

// serveGitTrustEntrypoint runs Nonce in a process it starts, which it keys
// on the call through callDigest, as a generated entrypoint does: the
// process serves an address and prints a new nonce, or the error serving
// gave it.
var serveGitTrustEntrypoint = fmt.Sprintf(`type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Epcaller")
        .withConstructor(function("", typeDef.withObject("Epcaller")))
        .withFunction(function("Nonce", typeDef.withKind(TypeDefKind.STRING_KIND))),
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
      (JSON.encode(
        container
          .from(%q)
          .withEnvVariable("DAGGER_MODULE_CALL_DIGEST", callDigest ?? "")
          .withFile("/serve.sh", currentModule.source.file("serve.sh"))
          .withExec(["sh", "/serve.sh"], experimentalPrivilegedNesting: true)
          .stdout
      ) :: JSON!)
    }
  }
}
`, alpineImage)

// serveGitTrustEntrypointScript is the process: it serves address and prints
// a new nonce, or what serving answered when it failed.
func serveGitTrustEntrypointScript(address string) string {
	return fmt.Sprintf(`set -e
apk add -q curl
q() { curl -s -u "$DAGGER_SESSION_TOKEN:" -H 'Content-Type: application/json' -d "$1" "http://127.0.0.1:$DAGGER_SESSION_PORT/query"; }
out=$(q '{"query":"{serveModule(address: \"%s\")}"}')
case "$out" in
  *errors*) printf '%%s' "$out" ;;
  *) date +%%s%%N | tr -d '\n' ;;
esac
`, address)
}

// serveGitTrustRepo serves content as a repository anyone may read, and
// returns its URL by IP so nested sessions reach it as the engine does.
func serveGitTrustRepo(ctx context.Context, t *testctx.T, c *dagger.Client, content *core.Directory, token *core.Secret) string {
	t.Helper()

	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, content, "main"), "", token)
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	host, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)
	out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"getent", "hosts", host}).Stdout(ctx)
	require.NoError(t, err)
	fields := strings.Fields(out)
	require.NotEmpty(t, fields, "unexpected getent output: %q", out)
	return "http://" + fields[0] + "/repo.git"
}

// serveGitTrustPrivateRepo serves a repository that requires credentials,
// holding a hello module and another module, and returns its URL by IP.
func serveGitTrustPrivateRepo(ctx context.Context, t *testctx.T, c *dagger.Client) string {
	t.Helper()

	return serveGitTrustRepo(ctx, t, c, serveGitTrustRepoContent(c), core.NewQuery(c).SetSecret("serve-git-trust", serveGitTrustToken))
}

// serveGitTrustRepoContent holds a hello module and another module.
func serveGitTrustRepoContent(c *dagger.Client) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("hello/dagger-module.toml", serveModuleHelloManifest).
		WithNewFile("hello/main.dang", serveModuleHelloSource).
		WithNewFile("other/dagger-module.toml", strings.ReplaceAll(serveModuleHelloManifest, `"hello"`, `"other"`)).
		WithNewFile("other/main.dang", strings.ReplaceAll(serveModuleHelloSource, "Hello", "Other"))
}

// serveGitTrustBase is a CLI container whose git credential helper answers
// for the private repository's host.
func serveGitTrustBase(t *testctx.T, c *dagger.Client, repoURL string) *core.Container {
	t.Helper()

	host := strings.TrimSuffix(repoURL, "/repo.git")
	helper := fmt.Sprintf(`!f() { test "$1" = get && printf 'username=x-access-token\npassword=%s\n'; }; f`, serveGitTrustToken)
	return goGitBase(t, c).
		WithExec([]string{"git", "config", "--global", "credential." + host + ".helper", helper})
}

// serveGitTrustWorkspaceConfig declares the caller module, and client as its
// one git client unless it is empty.
func serveGitTrustWorkspaceConfig(client string) string {
	cfg := `[modules.caller]
source = "modules/caller"

[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes."modules/caller"]
is-module = true
`
	if client != "" {
		cfg += fmt.Sprintf("clients = [%q]\n", client)
	}
	return cfg
}

func (ModuleLoadingSuite) TestServeModuleDeclaredGitClient(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	repoURL := serveGitTrustPrivateRepo(ctx, t, c)
	hello := repoURL + "/hello"

	hostCaller := func(client string) *core.Container {
		return serveGitTrustBase(t, c, repoURL).
			WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(client)).
			WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
			WithNewFile("modules/caller/main.go", serveGitTrustCallerSource)
	}

	t.Run("module serves a client its config declares", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller(hello).
			With(daggerCallAt("caller", "message", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})

	t.Run("module serving an undeclared client gets a hint", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller(hello).
			With(daggerCallAt("caller", "attempt", "--address="+repoURL+"/other")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "git authentication failed")
		require.Contains(t, out, `declare it as a client of module "caller"'s scope`)
	})

	t.Run("nothing declared fails closed", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller("").
			With(daggerCallAt("caller", "attempt", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "git authentication failed")
		require.Contains(t, out, `declare it as a client of module "caller"'s scope`)
	})

	t.Run("a removed declaration misses the cache", func(ctx context.Context, t *testctx.T) {
		// The declaration sits above the module's source root, outside the
		// module's implementation digest, which the test checks stays put.
		run := func(ctr *core.Container, n string, with core.WithContainerFunc) string {
			out, err := ctr.WithEnvVariable("SERVE_MODULE_RUN", n).With(with).Stdout(ctx)
			require.NoError(t, err)
			return out
		}
		nonce := daggerCallAt("caller", "nonce", "--address="+hello)
		digest := daggerQuery(`{currentWorkspace{moduleSource(path: "/modules/caller"){digest}}}`)

		ctr := hostCaller(hello).WithNewFile("modules/caller/nonce.go", serveGitTrustNonceSource)
		first := run(ctr, "1", nonce)
		require.Regexp(t, `^[0-9]+\s*$`, first)
		sourceDigest := run(ctr, "2", digest)
		require.Equal(t, first, run(ctr, "3", nonce))

		ctr = ctr.WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(""))
		require.JSONEq(t, sourceDigest, run(ctr, "4", digest))
		require.Contains(t, run(ctr, "5", nonce), `declare it as a client of module "caller"'s scope`)
	})

	t.Run("another session without credentials misses the cache", func(ctx context.Context, t *testctx.T) {
		withNonce := func(ctr *core.Container) *core.Container {
			return ctr.
				WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(hello)).
				WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
				WithNewFile("modules/caller/main.go", serveGitTrustCallerSource).
				WithNewFile("modules/caller/nonce.go", serveGitTrustNonceSource).
				With(daggerCallAt("caller", "nonce", "--address="+hello))
		}
		first, err := serveGitTrustBase(t, connect(ctx, t), repoURL).With(withNonce).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `^[0-9]+\s*$`, first)

		out, err := goGitBase(t, connect(ctx, t)).With(withNonce).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "git authentication failed")
	})

	// sessionCaller is the caller with every function, declaring client, in
	// a CLI whose git credentials answer for the private repository when
	// withCredentials is set.
	sessionCaller := func(c *dagger.Client, client string, withCredentials bool) *core.Container {
		base := goGitBase(t, c)
		if withCredentials {
			base = serveGitTrustBase(t, c, repoURL)
		}
		return base.
			WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(client)).
			WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
			WithNewFile("modules/caller/main.go", serveGitTrustCallerSource).
			WithNewFile("modules/caller/nonce.go", serveGitTrustNonceSource).
			WithNewFile("modules/caller/session.go", serveGitTrustSessionSource)
	}
	stdout := func(ctx context.Context, t *testctx.T, ctr *core.Container) string {
		t.Helper()
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	nonceDir := func(address string) core.WithContainerFunc {
		return daggerCallAt("caller", "nonce-dir", "--address="+address, "file", "--path=nonce", "contents")
	}

	t.Run("a call that borrows no credential hits in another session", func(ctx context.Context, t *testctx.T) {
		// The declared client needs no credentials: serving it lends none.
		publicURL := serveGitTrustRepo(ctx, t, c, serveGitTrustRepoContent(c), nil)
		public := publicURL + "/hello"
		// Each run's CLI executes anew, so an equal output is the
		// function's result served from cache.
		run := func(c *dagger.Client, n string) (string, string) {
			ctr := sessionCaller(c, public, false).WithEnvVariable("SERVE_MODULE_RUN", n)
			return stdout(ctx, t, ctr.With(daggerCallAt("caller", "plain"))), stdout(ctx, t, ctr.With(daggerCallAt("caller", "nonce", "--address="+public)))
		}
		firstPlain, firstServed := run(connect(ctx, t), "1")
		require.Regexp(t, `^[0-9]+\s*$`, firstPlain)
		require.Regexp(t, `^[0-9]+\s*$`, firstServed)
		secondPlain, secondServed := run(connect(ctx, t), "2")
		t.Logf("plain: %q then %q; served: %q then %q", firstPlain, secondPlain, firstServed, secondServed)
		require.Equal(t, firstPlain, secondPlain)
		require.Equal(t, firstServed, secondServed)
	})

	t.Run("a result built with credentials misses in a session without them", func(ctx context.Context, t *testctx.T) {
		// The directory is a result the engine holds under its own key, not
		// only under the function's.
		first := stdout(ctx, t, sessionCaller(connect(ctx, t), hello, true).With(nonceDir(hello)))
		require.Regexp(t, `^[0-9]+\s*$`, first)

		out := stdout(ctx, t, sessionCaller(connect(ctx, t), hello, false).With(nonceDir(hello)))
		require.Contains(t, out, "git authentication failed")
	})

	t.Run("a result built with credentials hits in a session holding them", func(ctx context.Context, t *testctx.T) {
		first := stdout(ctx, t, sessionCaller(connect(ctx, t), hello, true).With(daggerCallAt("caller", "nonce", "--address="+hello)))
		require.Regexp(t, `^[0-9]+\s*$`, first)

		// The second session holds the same token once serving the client
		// has lent it; a session is served what requires a credential only
		// after it holds it.
		ctr := sessionCaller(connect(ctx, t), hello, true).WithEnvVariable("SERVE_MODULE_RUN", "2").With(daggerCallAt("caller", "attempt", "--address="+hello))
		require.Equal(t, "served", stdout(ctx, t, ctr))
		require.Equal(t, first, stdout(ctx, t, ctr.With(daggerCallAt("caller", "nonce", "--address="+hello))))
	})

	t.Run("a result relayed from a module that used credentials misses in a session without them", func(ctx context.Context, t *testctx.T) {
		// The caller returns a string the helper computed with the
		// credentials lent to it, so nothing in the caller's result depends
		// on them.
		relayCaller := func(c *dagger.Client, withCredentials bool) *core.Container {
			return sessionCaller(c, hello, withCredentials).
				WithNewFile("dagger.toml", serveGitTrustRelayConfig(hello)).
				WithNewFile("modules/caller/relay.go", serveGitTrustRelaySource).
				WithNewFile("modules/helper/dagger.json", serveGitTrustHelperManifest).
				WithNewFile("modules/helper/main.go", serveGitTrustHelperSource)
		}
		relay := func(fn string) core.WithContainerFunc {
			return daggerCallAt("caller", fn, "--address="+hello)
		}
		ctr := relayCaller(connect(ctx, t), true).With(relay("relay"))
		first := stdout(ctx, t, ctr)
		require.Regexp(t, `^[0-9]+\s*$`, first)
		// The helper's result comes from cache in this call's work.
		require.Equal(t, first, stdout(ctx, t, ctr.With(relay("relay-again"))))

		// The helper's touch borrows the credentials and returns nothing; the
		// caller returns a nonce of its own once it succeeded.
		touched := stdout(ctx, t, ctr.With(relay("touched")))
		require.Regexp(t, `^[0-9]+\s*$`, touched)

		ctr = relayCaller(connect(ctx, t), false)
		require.Contains(t, stdout(ctx, t, ctr.With(relay("relay"))), "git authentication failed")
		require.Contains(t, stdout(ctx, t, ctr.With(relay("relay-again"))), "git authentication failed")
		require.Contains(t, stdout(ctx, t, ctr.With(relay("touched"))), "git authentication failed")
	})

	t.Run("an entrypoint's process that used credentials misses in a session without them", func(ctx context.Context, t *testctx.T) {
		// The process serves the client; the call's result is what the
		// process printed, and the exec that printed it is a result of its own.
		epCaller := func(c *dagger.Client, withCredentials bool, n string) *core.Container {
			base := goGitBase(t, c)
			if withCredentials {
				base = serveGitTrustBase(t, c, repoURL)
			}
			return base.
				WithEnvVariable("SERVE_MODULE_RUN", n).
				WithNewFile("dagger.toml", fmt.Sprintf(`[modules.epcaller]
source = "modules/epcaller"

[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes."modules/epcaller"]
is-module = true
clients = [%q]
`, hello)).
				WithNewFile("modules/epcaller/dagger-module.toml", serveGitTrustEntrypointManifest).
				WithNewFile("modules/epcaller/entrypoint/main.dang", serveGitTrustEntrypoint).
				WithNewFile("modules/epcaller/serve.sh", serveGitTrustEntrypointScript(hello)).
				With(daggerCallAt("epcaller", "nonce"))
		}
		first := stdout(ctx, t, epCaller(connect(ctx, t), true, "1"))
		require.Regexp(t, `^[0-9]+\s*$`, first)

		require.Contains(t, stdout(ctx, t, epCaller(connect(ctx, t), false, "2")), "git authentication failed")
	})

	t.Run("a plain program keeps its own credentials", func(ctx context.Context, t *testctx.T) {
		out, err := hostCaller("").
			With(daggerQuery(`{serveModule(address: %q)}`, hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"serveModule": null}`, out)
	})

	t.Run("module loaded from git serves a client its own config declares", func(ctx context.Context, t *testctx.T) {
		// The caller's repository holds the declaration, not the workspace
		// calling it, which declares nothing.
		callerRepo := core.NewQuery(c).Directory().
			WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(hello)).
			WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
			WithNewFile("modules/caller/main.go", serveTreeCallerSource)
		gitDaemon, callerURL := gitService(ctx, t, c, callerRepo)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)

		out, err := serveGitTrustBase(t, c, repoURL).
			WithServiceBinding(gitHost, gitDaemon).
			WithNewFile("dagger.toml", "\n").
			With(daggerCallAt(callerURL+"#main:modules/caller", "message", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})

	t.Run("module in a git workspace serves a client the workspace declares", func(ctx context.Context, t *testctx.T) {
		// The workspace is the caller's repository, selected by its git ref.
		workspaceRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
			WithNewFile("dagger.toml", serveGitTrustWorkspaceConfig(hello)).
			WithNewFile("modules/caller/dagger.json", serveTreeCallerManifest).
			WithNewFile("modules/caller/main.go", serveTreeCallerSource))

		out, err := serveGitTrustBase(t, c, repoURL).
			With(daggerExec("-W", workspaceRef, "call", "caller", "message", "--address="+hello)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from hello", out)
	})
}
