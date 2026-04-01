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

func (UpSuite) TestUpRunService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := upTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-services")

	// Run a single service via "dagger up web" to verify it starts correctly.
	out, err := modGen.
		With(daggerExec("up", "web")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	_ = out
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
