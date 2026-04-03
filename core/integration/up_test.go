package core

import (
	"context"
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
	// dag.CurrentWorkspace().Services().List() to verify services are visible
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
	// respond, verify the nginx welcome page, then stop. A timeout ensures
	// the test never hangs.
	out, err := modGen.
		WithExec([]string{"sh", "-c", `
			# Start dagger up in the background
			dagger up web &
			DAGGER_PID=$!

			# Poll until the tunneled port responds (nginx on port 80)
			TIMEOUT=120
			ELAPSED=0
			while ! wget -q --spider http://localhost:80 2>/dev/null; do
				sleep 2
				ELAPSED=$((ELAPSED + 2))
				if [ "$ELAPSED" -ge "$TIMEOUT" ]; then
					echo "TIMEOUT: service did not become ready within ${TIMEOUT}s"
					kill $DAGGER_PID 2>/dev/null
					exit 1
				fi
			done

			# Verify the service responds with nginx content
			BODY=$(wget -qO- http://localhost:80 2>/dev/null)
			echo "$BODY" | grep -qi "nginx" || {
				echo "FAIL: expected nginx response, got: $BODY"
				kill $DAGGER_PID 2>/dev/null
				exit 1
			}

			echo "OK: service responded"
			kill $DAGGER_PID 2>/dev/null
			wait $DAGGER_PID 2>/dev/null
			exit 0
		`}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
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
		WithExec([]string{"sh", "-c", `
			# Start dagger up in the background
			dagger up &
			DAGGER_PID=$!

			# Poll until the remapped port responds (nginx on port 3000)
			TIMEOUT=120
			ELAPSED=0
			while ! wget -q --spider http://localhost:3000 2>/dev/null; do
				sleep 2
				ELAPSED=$((ELAPSED + 2))
				if [ "$ELAPSED" -ge "$TIMEOUT" ]; then
					echo "TIMEOUT: service did not become ready within ${TIMEOUT}s"
					kill $DAGGER_PID 2>/dev/null
					exit 1
				fi
			done

			# Verify the service responds with nginx content on the remapped port
			BODY=$(wget -qO- http://localhost:3000 2>/dev/null)
			echo "$BODY" | grep -qi "nginx" || {
				echo "FAIL: expected nginx response on port 3000, got: $BODY"
				kill $DAGGER_PID 2>/dev/null
				exit 1
			}

			echo "OK: port mapping works"
			kill $DAGGER_PID 2>/dev/null
			wait $DAGGER_PID 2>/dev/null
			exit 0
		`}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
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
