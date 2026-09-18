package core

// These tests cover top-level `dagger up`, which starts services declared by a
// workspace or SDK. They verify direct SDK use, env services, port collisions,
// service binding, partial startup failures, workspace skip/port config, and
// toolchain use.
//
// See also:
// - module_up_test.go: module development server and interactive module UI.
// - services_test.go: core service lifecycle and networking.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type UpSuite struct{}

func TestUp(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(UpSuite{})
}

func upTestEnv(t *testctx.T, c *dagger.Client) (*dagger.Container, error) {
	return specificTestEnv(t, c, "services")
}

// daggerUpVerify prepares module definitions before timing service readiness.
func daggerUpVerify(upArgs, url, expectBodyContains, okMsg string, timeoutSecs int) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"sh", "-c", upVerifyScript(upArgs, url, expectBodyContains, okMsg, upVerifyBounds{
			prepare: 300, ready: timeoutSecs, probe: 5, shutdown: 30,
		})})
	}
}

func (UpSuite) TestUpDirectSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-services"},
		{"typescript", "hello-with-services-ts"},
		{"python", "hello-with-services-py"},
		{"java", "hello-with-services-java"},
		{"dang", "hello-with-services-dang"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen, err := upTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir(tc.path)
			// list services
			out, err := modGen.
				With(daggerExec("up", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "web")
			require.Contains(t, out, "redis")
			require.Contains(t, out, "infra:database")
		})
	}
}

func (UpSuite) TestUpEnvServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-services")

	// Call the module's WorkspaceServices function, which lists
	// Workspace.services via an auto-injected Workspace arg, to verify
	// services are visible from within the module execution context.
	out, err := modGen.
		With(daggerExec("call", "workspace-services")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "web")
	require.Contains(t, out, "redis")
	require.Contains(t, out, "infra:database")
}

func (UpSuite) TestUpNoServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)

	// An empty workspace must report the problem rather than wait for Ctrl+C.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := modGen.
		WithWorkdir("/empty").
		WithNewFile("dagger.toml", "").
		With(daggerExecFail("up")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "no services found")
}

func (UpSuite) TestUpPortCollision(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("port-collision")

	// Try to run all services — should fail with port collision error
	out, err := modGen.
		With(daggerExecFail("up")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "port collision")
	require.Contains(t, out, "8080")
}

func (UpSuite) TestUpValidationRejectsBadSignature(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("wrong return type", func(ctx context.Context, t *testctx.T) {
		modGen, err := upTestEnv(t, c)
		require.NoError(t, err)

		// badup-return's @up returns Container!, which must be rejected at module load.
		out, err := modGen.WithWorkdir("badup-return").
			With(daggerExecFail("up", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "@up functions must return the core Service! type")
	})

	t.Run("required arg", func(ctx context.Context, t *testctx.T) {
		modGen, err := upTestEnv(t, c)
		require.NoError(t, err)

		// badup-arg's @up declares a required `image: String!`, which must be
		// rejected at module load.
		out, err := modGen.WithWorkdir("badup-arg").
			With(daggerExecFail("up", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "@up functions must be callable with no arguments")
	})
}

func (UpSuite) TestUpServiceBinding(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("service-binding")

	// Verify both services are listed
	out, err := modGen.
		With(daggerExec("up", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "backend")
	require.Contains(t, out, "frontend")

	t.Run("single service with binding", func(ctx context.Context, t *testctx.T) {
		// Run "dagger up frontend" — frontend (nginx:80) depends on backend
		// (redis:6379) via withServiceBinding. Backend starts as an internal
		// service binding. Only frontend gets a host tunnel on port 80.
		out, err := modGen.
			With(daggerUpVerify("frontend", "http://localhost:80", "nginx",
				"OK: single service with binding works", 120)).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "OK: single service with binding works")
	})

	t.Run("all services with dedup", func(ctx context.Context, t *testctx.T) {
		// Run "dagger up" (all services). Backend (redis:6379) is both a
		// standalone +up service AND a service binding inside frontend
		// (nginx:80). Dagql dedup ensures only one backend instance runs.
		out, err := modGen.
			With(daggerUpVerify("", "http://localhost:80", "nginx",
				"OK: all services with dedup works", 180)).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "OK: all services with dedup works")
	})
}

func (UpSuite) TestUpModuleWiring(ctx context.Context, t *testctx.T) {
	// A module's constructor can receive another module's function output via a
	// plain-string module reference in workspace settings:
	// settings.<arg> = "<module>:<function>". These strings route through the
	// Address decoders (see core/schema/address.go: resolveModuleRef, wired
	// into the corresponding Address object decoders). Supported for Service
	// (+up functions), Container, Directory, File, and Workspace. The same
	// decoders back CLI object flags, so a constructor arg like
	// --app=<module>:<function> resolves identically.
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	// Optional Workspace args inherit the ambient workspace when no setting is
	// configured. Keep its marker valid so the shared consumer fixture can
	// derive a File in every subtest; the wiring case overrides this workspace
	// with container-provider:workspace and observes a different marker.
	modGen = modGen.WithNewFile("app/marker.txt", "ambient")

	t.Run("without refs", func(ctx context.Context, t *testctx.T) {
		ctr := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
`)
		out, err := ctr.
			With(daggerExec("call", "service-ref-consumer", "has-service")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "false")

		out, err = ctr.
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "none")
	})

	t.Run("service ref via settings", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.hello-with-services]
source = "../hello-with-services"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "dag://hello-with-services/web"
`).
			With(daggerExec("call", "service-ref-consumer", "has-service")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "true")
	})

	t.Run("container ref via settings", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://container-provider/image"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-provider")
	})

	t.Run("entrypoint module ref via settings", func(ctx context.Context, t *testctx.T) {
		// The referenced module is the workspace entrypoint.
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"
entrypoint = true

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://container-provider/image"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-provider")

		// Filtered check: only the consumer loads up front, so the entrypoint
		// module must be demand-loaded when the wiring resolves.
		out, err = modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.hello-with-services]
source = "../hello-with-services"
entrypoint = true

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "dag://hello-with-services/web"
`).
			With(daggerExec("check", "service-ref-consumer:check-service")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "check-service")
	})

	t.Run("short-form entrypoint ref via settings", func(ctx context.Context, t *testctx.T) {
		// A "dag://<function>" address names a function of the entrypoint module.
		ctr := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"
entrypoint = true

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://image"
settings.file = "marker.txt"
`)
		out, err := ctr.
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-provider")

		// A value without the scheme keeps its address meaning, even though
		// the entrypoint defines a "file" function.
		out, err = ctr.
			With(daggerExec("call", "service-ref-consumer", "file-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ambient", strings.TrimSpace(out))
	})

	t.Run("settings stores entrypoint refs as DAG addresses", func(ctx context.Context, t *testctx.T) {
		// The short form is accepted but the full address is written; a string
		// setting is never rewritten.
		ctr := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"
entrypoint = true

[modules.service-ref-consumer]
source = "../service-ref-consumer"
`).
			With(daggerExec("settings", "service-ref-consumer", "base", "image")).
			With(daggerExec("settings", "service-ref-consumer", "label", "image"))

		cfg, err := ctr.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, cfg, `base = "dag://container-provider/image"`)
		require.Contains(t, cfg, `label = "image"`)

		out, err := ctr.
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-provider")

		cfg, err = ctr.
			With(daggerExec("settings", "service-ref-consumer", "base", "dag://container-provider/image")).
			File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, cfg, `base = "dag://container-provider/image"`)
	})

	t.Run("artifact refs via settings", func(ctx context.Context, t *testctx.T) {
		ctr := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.directory = "dag://container-provider/directory"
settings.file = "dag://container-provider/file"
settings.sourceWorkspace = "dag://container-provider/workspace"
`)

		out, err := ctr.
			With(daggerExec("call", "service-ref-consumer", "directory-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "container-provider", strings.TrimSpace(out))

		out, err = ctr.
			With(daggerExec("call", "service-ref-consumer", "file-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "container-provider", strings.TrimSpace(out))

		out, err = ctr.
			With(daggerExec("call", "service-ref-consumer", "workspace-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "container-provider", strings.TrimSpace(out))
	})

	t.Run("provider with required Workspace arg", func(ctx context.Context, t *testctx.T) {
		// The referenced module declares a required Workspace! — on its
		// constructor (image) and on the function itself (image-for). The
		// engine evaluates the artifact by hand, so it must supply that
		// workspace before dagql's non-null check; the injection hook that
		// fills optional Workspace args runs too late to help (see
		// core/modtree.go boundWorkspaceArgs). Regression: after
		// Workspace args stopped being published as nullable, this failed
		// with `missing required argument: "ws"`.
		//
		// The provider bakes the workspace's marker.txt into PROVIDED_BY,
		// proving it received the caller's workspace and not just any one.
		ctr := modGen.
			WithWorkdir("app").
			WithNewFile("marker.txt", "app-workspace")
		for _, fn := range []string{"image", "image-for"} {
			t.Run(fn, func(ctx context.Context, t *testctx.T) {
				out, err := ctr.
					WithNewFile("dagger.toml", `[modules.workspace-container-provider]
source = "../workspace-container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://workspace-container-provider/`+fn+`"
`).
					With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
					Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "workspace-container-provider:app-workspace")
			})
		}
	})

	t.Run("service ref via CLI flag", func(ctx context.Context, t *testctx.T) {
		// The consumer's constructor arg (app *dagger.Service) is settable as a
		// flag on the call; the flag value routes through the same Address
		// decoders as settings strings.
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.hello-with-services]
source = "../hello-with-services"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
`).
			With(daggerExec("call", "service-ref-consumer", "--app=dag://hello-with-services/web", "has-service")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "true")
	})

	t.Run("service ref via settings under check", func(ctx context.Context, t *testctx.T) {
		// Checks run through the ModTree path (standalone per-module dagql
		// servers), not the caller's session schema, so settings-wired
		// constructor args must resolve against the schema served to the main
		// client (see UserDefault.Value in core/modfunc.go). The consumer's
		// check-service check passes only when the wired service arrived.
		ctr := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.hello-with-services]
source = "../hello-with-services"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "dag://hello-with-services/web"
`)

		// Unfiltered: every workspace module loads before the check runs.
		out, err := ctr.
			With(daggerExec("check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "check-service")

		// Filtered: only the named module loads up front; the referenced
		// module (hello-with-services) must be loaded when the wiring
		// resolves (see resolveWorkspaceArtifact in core/schema/address.go).
		out, err = ctr.
			With(daggerExec("check", "service-ref-consumer:check-service")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "check-service")
	})

	t.Run("dag address is a hard error", func(ctx context.Context, t *testctx.T) {
		// A dag:// address never falls back to external resolution: an unknown
		// function is a hard error and must NOT pull an image named
		// "container-provider". See resolveModuleRef in core/schema/address.go.
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://container-provider/nonexistent"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Sync(ctx)
		requireErrOut(t, err, `resolve "dag://container-provider/nonexistent": no artifact matches`)
	})

	t.Run("unknown nested field is a hard error", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://container-provider/image/extra"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Sync(ctx)
		requireErrOut(t, err, `resolve "dag://container-provider/image/extra": no artifact matches`)
	})

	t.Run("rejects reference cycle", func(ctx context.Context, t *testctx.T) {
		// A self-referential module ref (container-provider's own base wired
		// from dag://container-provider/image) is now caught by the cycle guard in
		// core/schema/address.go (resolveModuleRef, moduleRefCycleKey) and
		// fails fast with a "module reference cycle detected" error. Before the
		// guard existed this recursed unboundedly and hung the engine, so a
		// context deadline is kept as a safety net against a regression wedging CI.
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"
settings.base = "dag://container-provider/image"
`).
			With(daggerExec("call", "container-provider", "image", "env-variable", "--name", "PROVIDED_BY")).
			Sync(ctx)
		requireErrOut(t, err, "module reference cycle detected")
	})

	t.Run("value without scheme is an image", func(ctx context.Context, t *testctx.T) {
		// "alpine:3.20" has no dag:// scheme, so it keeps its external meaning.
		// The image is a plain alpine with no PROVIDED_BY annotation, so
		// container-provided-by returns an empty value rather than
		// "container-provider" — it must NOT be treated as a module ref.
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "alpine:3.20"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "container-provider")
	})

	t.Run("core field is not a module ref (container)", func(ctx context.Context, t *testctx.T) {
		// "git:2.40" has no dag:// scheme, so it is an image reference even
		// though "git" names a core Query field. There is no "git:2.40" image,
		// so the failure must be an image-resolution error, never a workspace
		// lookup. (A near-miss hint mentioning wiring in another module's output
		// is acceptable — what must be absent is a workspace resolution failure
		// that would mean "git" was treated as a module.)
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "git:2.40"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Sync(ctx)
		require.Error(t, err)
		var execErr *dagger.ExecError
		combined := err.Error()
		if errors.As(err, &execErr) {
			combined = fmt.Sprintf("%s\n%s\n%s", err, execErr.Stdout, execErr.Stderr)
		}
		// Must NOT have been committed as a module ref (that path emits
		// "resolve module reference %q (module %q)").
		require.NotContains(t, combined, "no artifact matches")
		require.NotContains(t, combined, `module "git"`)
	})

	t.Run("core field is not a module ref (service, gives hint)", func(ctx context.Context, t *testctx.T) {
		// "secret:foo" has no dag:// scheme, so it is a service URL, which fails
		// to parse. Because it is shaped like the old module reference form,
		// the hint shows the dag:// spelling rather than a raw "missing port in
		// address".
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "secret:foo"
`).
			With(daggerExec("call", "service-ref-consumer", "has-service")).
			Sync(ctx)
		requireErrOut(t, err, "write it as a DAG address: dag://secret/foo")
	})

	t.Run("old module reference form gives hint", func(ctx context.Context, t *testctx.T) {
		// "docusarus:serve" is shaped like the old module reference form but
		// has no dag:// scheme, so it is a service URL. The fallback error is
		// wrapped with a hint showing the dag:// spelling.
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "docusarus:serve"
`).
			With(daggerExec("call", "service-ref-consumer", "has-service")).
			Sync(ctx)
		requireErrOut(t, err, "write it as a DAG address: dag://docusarus/serve")
	})

	t.Run("rejects two-module reference cycle", func(ctx context.Context, t *testctx.T) {
		// A→B→A: container-provider's base is wired from service-ref-consumer/ctr,
		// and service-ref-consumer's base is wired from container-provider/image.
		// The cycle guard must catch this and fail fast rather than hang. A context
		// deadline is kept as a safety net against a regression wedging CI.
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"
settings.base = "dag://service-ref-consumer/ctr"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://container-provider/image"
`).
			With(daggerExec("call", "container-provider", "image", "env-variable", "--name", "PROVIDED_BY")).
			Sync(ctx)
		requireErrOut(t, err, "module reference cycle detected")
	})

	t.Run("install alias names the leading segment", func(ctx context.Context, t *testctx.T) {
		// The leading path segment of a DAG address is the install NAME (the
		// [modules.X] key), which need not equal the module's own name. Here the
		// container-provider module is installed under the key "cp", so
		// "dag://cp/image" must resolve.
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.cp]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "dag://cp/image"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-provider")
	})
}

func (UpSuite) TestUpPartialStartupFailure(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("partial-failure")

	// Run all services. The "broken" service fails on startup while "healthy"
	// is already running. dagger up must cancel the healthy service and exit
	// with the startup error — not hang forever.
	out, err := modGen.
		With(daggerExecFail("up")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "startup failed")
}

func (UpSuite) TestUpRunService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-services")

	// Run "dagger up web" in the background, wait for the tunneled port to
	// respond, verify the nginx welcome page, then stop.
	out, err := modGen.
		With(daggerUpVerify("web", "http://localhost:80", "nginx",
			"OK: service responded", 120)).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "OK: service responded")
}

func (UpSuite) TestWorkspaceUpSkip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)

	ctr := modGen.WithNewFile("dagger.toml", `[modules.hello-with-services]
source = "hello-with-services"
up.skip = ["redis"]
`)

	out, err := ctr.With(daggerExec("up", "-l")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-services:web")
	require.NotContains(t, out, "hello-with-services:redis")
	require.Contains(t, out, "hello-with-services:infra:database")
}

func (UpSuite) TestWorkspaceUpPortMapping(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)

	ctr := modGen.WithNewFile("dagger.toml", `[modules.hello-with-services]
source = "hello-with-services"
up.skip = ["redis", "infra:database"]

[ports.3000]
backendService = "hello-with-services:web"
backendPort = 80
`)

	out, err := ctr.
		With(daggerUpVerify("", "http://localhost:3000", "nginx",
			"OK: port mapping works", 120)).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "OK: port mapping works")
}

func (UpSuite) TestUpAsToolchain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-services"},
		{"typescript", "hello-with-services-ts"},
		{"python", "hello-with-services-py"},
		{"java", "hello-with-services-java"},
		{"dang", "hello-with-services-dang"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// install hello-with-services as toolchain
			modGen, err := upTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir("app").
				WithNewFile("dagger.toml", fmt.Sprintf(`[modules.%s]
source = "../%s"
`, tc.path, tc.path))
			// list services
			out, err := modGen.
				With(daggerExec("up", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, tc.path+":web")
			require.Contains(t, out, tc.path+":redis")
			require.Contains(t, out, tc.path+":infra:database")
		})
	}
}
