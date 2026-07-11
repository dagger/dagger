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

// daggerUpVerify starts "dagger up" with the given args in the background,
// polls the given URL until it responds, verifies the body matches the
// expected content (case-insensitive grep), then stops the process.
// Returns a WithContainerFunc suitable for use with Container.WithExec.
func daggerUpVerify(upArgs, url, expectBodyContains, okMsg string, timeoutSecs int) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"sh", "-c", fmt.Sprintf(`
			dagger up %s &
			DAGGER_PID=$!

			TIMEOUT=%d
			ELAPSED=0
			while ! wget -q --spider %s 2>/dev/null; do
				sleep 2
				ELAPSED=$((ELAPSED + 2))
				if [ "$ELAPSED" -ge "$TIMEOUT" ]; then
					echo "TIMEOUT: service did not become ready within ${TIMEOUT}s"
					kill $DAGGER_PID 2>/dev/null
					exit 1
				fi
			done

			BODY=$(wget -qO- %s 2>/dev/null)
			echo "$BODY" | grep -qi "%s" || {
				echo "FAIL: expected %s in response, got: $BODY"
				kill $DAGGER_PID 2>/dev/null
				exit 1
			}

			echo "%s"
			kill $DAGGER_PID 2>/dev/null
			wait $DAGGER_PID 2>/dev/null
			exit 0
		`, upArgs, timeoutSecs, url, url, expectBodyContains, expectBodyContains, okMsg,
		)}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
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

	// Call the module's CurrentEnvServices function which queries
	// dag.CurrentEnv().Services().List() to verify services are visible
	// from within the module execution context.
	out, err := modGen.
		With(daggerExec("call", "current-env-services")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "web")
	require.Contains(t, out, "redis")
	require.Contains(t, out, "infra:database")
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
	// into .service() and .container()). Supported for Service (+up functions)
	// and Container. The same decoders back CLI object flags, so a constructor
	// arg like --app=<module>:<function> resolves identically.
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)

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
settings.app = "hello-with-services:web"
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
settings.base = "container-provider:image"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-provider")
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
			With(daggerExec("call", "service-ref-consumer", "--app=hello-with-services:web", "has-service")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "true")
	})

	t.Run("commit-on-match is a hard error", func(ctx context.Context, t *testctx.T) {
		// Once the first segment names an installed module, the ref is committed:
		// an unknown function is a hard error and must NOT fall back to pulling an
		// image named "container-provider". See resolveModuleRef in
		// core/schema/address.go.
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "container-provider:nonexistent"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Sync(ctx)
		requireErrOut(t, err, "module reference")
		requireErrOut(t, err, "container-provider")
	})

	t.Run("extra segments error", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "container-provider:image:extra"
`).
			With(daggerExec("call", "service-ref-consumer", "container-provided-by")).
			Sync(ctx)
		requireErrOut(t, err, "only container-provider:<function> is supported today")
	})

	t.Run("rejects reference cycle", func(ctx context.Context, t *testctx.T) {
		// A self-referential module ref (container-provider's own base wired
		// from container-provider:image) is now caught by the cycle guard in
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
settings.base = "container-provider:image"
`).
			With(daggerExec("call", "container-provider", "image", "env-variable", "--name", "PROVIDED_BY")).
			Sync(ctx)
		requireErrOut(t, err, "module reference cycle detected")
	})

	t.Run("OCI fallback when no module matches", func(ctx context.Context, t *testctx.T) {
		// "alpine:3.20" has no installed module named "alpine", so it falls
		// through to image interpretation (precedence rule's fallback side). The
		// image is a plain alpine with no PROVIDED_BY annotation, so
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
		// With NO module installed, "git:2.40" happens to name a core Query field
		// ("git"), which exists on the outer root too. It must NOT be committed as
		// a module ref; it falls through to image resolution. There is no
		// "git:2.40" image, so the failure must be an image-resolution error, NOT
		// a committed "resolve module reference" error. See F1 in
		// resolveModuleRef. (A near-miss F2 hint mentioning wiring in another
		// module's output is acceptable — what must be absent is the
		// committed-ref failure that would mean "git" was treated as a module.)
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
		require.NotContains(t, combined, "resolve module reference")
		require.NotContains(t, combined, `module "git"`)
	})

	t.Run("core field is not a module ref (service, gives hint)", func(ctx context.Context, t *testctx.T) {
		// "secret:foo" names the core "secret" field, so it is not a module
		// ref. It falls through to service URL parsing, which fails. Because it is
		// bare-ref-shaped, the F2 hint is appended rather than a raw "missing port
		// in address".
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "secret:foo"
`).
			With(daggerExec("call", "service-ref-consumer", "has-service")).
			Sync(ctx)
		requireErrOut(t, err, "no installed module matches")
		requireErrOut(t, err, "dagger.toml")
	})

	t.Run("typo'd module name gives near-miss hint", func(ctx context.Context, t *testctx.T) {
		// "docusarus:serve" is bare-ref-shaped but names no installed module (and
		// "docusarus" is not a core field), so it falls through. The fallback
		// error is wrapped with the F2 hint pointing at dagger.toml.
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.app = "docusarus:serve"
`).
			With(daggerExec("call", "service-ref-consumer", "has-service")).
			Sync(ctx)
		requireErrOut(t, err, `no installed module matches "docusarus:serve"`)
		requireErrOut(t, err, "check the [modules.X] keys in dagger.toml")
	})

	t.Run("rejects two-module reference cycle", func(ctx context.Context, t *testctx.T) {
		// A→B→A: container-provider's base is wired from service-ref-consumer:ctr,
		// and service-ref-consumer's base is wired from container-provider:image.
		// The cycle guard must catch this and fail fast rather than hang. A context
		// deadline is kept as a safety net against a regression wedging CI.
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		_, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.container-provider]
source = "../container-provider"
settings.base = "service-ref-consumer:ctr"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "container-provider:image"
`).
			With(daggerExec("call", "container-provider", "image", "env-variable", "--name", "PROVIDED_BY")).
			Sync(ctx)
		requireErrOut(t, err, "module reference cycle detected")
	})

	t.Run("install alias names the leading segment", func(ctx context.Context, t *testctx.T) {
		// The leading segment of a module ref is the install NAME (the
		// [modules.X] key), which need not equal the module's own name. Here the
		// container-provider module is installed under the key "cp", so
		// "cp:image" must resolve. See F4.
		out, err := modGen.
			WithWorkdir("app").
			WithNewFile("dagger.toml", `[modules.cp]
source = "../container-provider"

[modules.service-ref-consumer]
source = "../service-ref-consumer"
settings.base = "cp:image"
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
