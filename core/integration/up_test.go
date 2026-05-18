package core

import (
	"context"
	"fmt"
	"testing"

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

func (UpSuite) TestUpServiceRef(ctx context.Context, t *testctx.T) {
	// Test that a module's constructor can receive a Service from another
	// module's +up function via a { "from": "<module>:<function>" } config.
	c := connect(ctx, t)

	modGen := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		// Create the provider module — a +up function that returns a Service
		WithWorkdir("/work/provider").
		With(daggerExec("init", "--sdk=go", "--name=provider")).
		WithNewFile("main.go", `package main

import "dagger/provider/internal/dagger"

type Provider struct{}

// +up
func (m *Provider) Serve() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithExposedPort(80).
		AsService()
}
`).
		// Create the consumer module — accepts optional Service constructor arg
		WithWorkdir("/work/consumer").
		With(daggerExec("init", "--sdk=go", "--name=consumer")).
		WithNewFile("main.go", `package main

import (
	"context"
	"dagger/consumer/internal/dagger"
)

type Consumer struct {
	App *dagger.Service
}

func New(
	// +optional
	app *dagger.Service,
) *Consumer {
	return &Consumer{App: app}
}

// HasService returns "true" if a Service was provided, "false" otherwise.
func (m *Consumer) HasService(ctx context.Context) (string, error) {
	if m.App == nil {
		return "false", nil
	}
	return "true", nil
}
`).
		// Create the main module that wires them together
		WithWorkdir("/work/main").
		With(daggerExec("init")).
		WithNewFile("dagger.json", `{
  "name": "main",
  "engineVersion": "v0.20.1",
  "toolchains": [
    {
      "name": "provider",
      "source": "../provider"
    },
    {
      "name": "consumer",
      "source": "../consumer",
      "customizations": [
        {
          "argument": "app",
          "default": {"from": "provider:serve"}
        }
      ]
    }
  ]
}
`)

	// Verify the consumer received the service from the provider
	out, err := modGen.
		With(daggerExec("call", "consumer", "has-service")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "true")
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

func (UpSuite) TestUpIgnoreServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)

	// Install hello-with-services as a toolchain
	modGen = modGen.
		WithWorkdir("app").
		With(daggerExec("init")).
		With(daggerExec("toolchain", "install", "../hello-with-services"))

	// Verify all three services are listed before ignoring
	out, err := modGen.
		With(daggerExec("up", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-services:web")
	require.Contains(t, out, "hello-with-services:redis")
	require.Contains(t, out, "hello-with-services:infra:database")

	// Add ignoreServices to filter out redis
	modGen = modGen.WithNewFile("dagger.json", `{
  "name": "app",
  "engineVersion": "v0.16.0",
  "toolchains": [
    {
      "name": "hello-with-services",
      "source": "../hello-with-services",
      "ignoreServices": [
        "redis"
      ]
    }
  ]
}`)
	out, err = modGen.
		With(daggerExec("up", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-services:web")
	require.NotContains(t, out, "hello-with-services:redis")
	require.Contains(t, out, "hello-with-services:infra:database")
}

func (UpSuite) TestUpPortMapping(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)

	// Install hello-with-services as a toolchain with port mapping
	// Remap web from port 80 to host port 3000
	modGen = modGen.
		WithWorkdir("app").
		With(daggerExec("init")).
		WithNewFile("dagger.json", `{
  "name": "app",
  "engineVersion": "v0.16.0",
  "toolchains": [
    {
      "name": "hello-with-services",
      "source": "../hello-with-services",
      "portMappings": {
        "web": ["3000:80"]
      },
      "ignoreServices": [
        "redis",
        "infra:database"
      ]
    }
  ]
}`)

	// Run dagger up in the background with port mapping, verify service
	// is accessible on the remapped port.
	out, err := modGen.
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
				With(daggerExec("init")).
				With(daggerExec("toolchain", "install", "../"+tc.path))
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
