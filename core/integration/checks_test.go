package core

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ChecksSuite struct{}

func TestChecks(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ChecksSuite{})
}

func checksTestEnv(t *testctx.T, c *dagger.Client) (*dagger.Container, error) {
	return specificTestEnv(t, c, "checks")
}

func specificTestEnv(t *testctx.T, c *dagger.Client, subfolder string) (*dagger.Container, error) {
	// java SDK is not embedded in the engine, so we mount the java sdk to be able
	// to test non released features
	javaSdkSrc, err := filepath.Abs("../../sdk/java")
	if err != nil {
		return nil, err
	}
	return c.Container().
			From(alpineImage).
			// init git in a directory containing both the modules and the java SDK
			// that way dagger sees this directory as the root
			WithWorkdir("/work").
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithWorkdir("/work/modules/").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory(".", c.Host().Directory("./testdata/"+subfolder)).
			WithMountedDirectory("/work/sdk/java", c.Host().Directory(javaSdkSrc)).
			WithDirectory("app", c.Directory()),
		nil
}

func (ChecksSuite) TestChecksDirectSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-checks"},
		{"typescript", "hello-with-checks-ts"},
		{"python", "hello-with-checks-py"},
		{"java", "hello-with-checks-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir(tc.path)
			// list checks
			out, err := modGen.
				With(daggerExec("check", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "passing-check")
			require.Contains(t, out, "failing-check")
			require.Contains(t, out, "passing-container")
			require.Contains(t, out, "failing-container")
			require.Contains(t, out, "test:lint")
			require.Contains(t, out, "test:unit")
			// run a specific passing check
			out, err = modGen.
				With(daggerExec("--progress=report", "check", "passing*")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `passing-container.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", "failing*")).
				CombinedOutput(ctx)
			require.Regexp(t, "failing-check.*ERROR", out)
			require.Regexp(t, "failing-container.*ERROR", out)
			require.NoError(t, err)
			// run all checks
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `passing-container.*OK`, out)
			require.Regexp(t, "failing-check.*ERROR", out)
			require.Regexp(t, "failing-container.*ERROR", out)
			require.NoError(t, err)
		})
	}
}

func (ChecksSuite) TestChecksAsBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-checks"},
		{"typescript", "hello-with-checks-ts"},
		{"python", "hello-with-checks-py"},
		{"java", "hello-with-checks-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// install hello-with-checks as blueprint
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.WithWorkdir("app").
				With(daggerExec("init", "--blueprint", "../"+tc.path))
			// list checks
			out, err := modGen.
				With(daggerExec("check", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "passing-check")
			require.Contains(t, out, "failing-check")
			// run a specific passing check
			out, err = modGen.
				With(daggerExec("--progress=report", "check", "passing-check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", "failing-check")).
				CombinedOutput(ctx)
			require.Regexp(t, "failing-check.*ERROR", out)
			require.NoError(t, err)
			// run all checks
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `failing-check.*ERROR`, out)
			require.NoError(t, err)
		})
	}
}

func (ChecksSuite) TestChecksAsToolchain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-checks"},
		{"typescript", "hello-with-checks-ts"},
		{"python", "hello-with-checks-py"},
		{"java", "hello-with-checks-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// install hello-with-checks as toolchain
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir("app").
				With(daggerExec("init")).
				With(daggerExec("toolchain", "install", "../"+tc.path))
			// list checks
			out, err := modGen.
				With(daggerExec("check", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, tc.path+":passing-check")
			require.Contains(t, out, tc.path+":failing-check")
			require.Contains(t, out, tc.path+":test:lint")
			require.Contains(t, out, tc.path+":test:unit")
			// run a specific passing check
			out, err = modGen.
				With(daggerExec("--progress=report", "check", tc.path+":passing-check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", tc.path+":failing-check")).
				CombinedOutput(ctx)
			require.Regexp(t, `failing-check.*ERROR`, out)
			require.NoError(t, err)
			// run all checks
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `failing-check.*ERROR`, out)
			require.NoError(t, err)
		})
	}
}

func (ChecksSuite) TestCheckCacheBuster(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modDir := newFlakyChecksModule(ctx, t)

	mod, err := c.ModuleSource(modDir).AsModule().Sync(ctx)
	require.NoError(t, err)

	check := mod.Check("flaky")

	run1 := check.Run()
	msg1, err := run1.Error().Message(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msg1)

	run2 := check.Run()
	msg2, err := run2.Error().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, msg1, msg2)

	run3 := check.Run(dagger.CheckRunOpts{CacheBuster: "buster-a"})
	msg3, err := run3.Error().Message(ctx)
	require.NoError(t, err)
	require.NotEqual(t, msg1, msg3)

	run4 := check.Run(dagger.CheckRunOpts{CacheBuster: "buster-a"})
	msg4, err := run4.Error().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, msg3, msg4)

	run5 := check.Run(dagger.CheckRunOpts{CacheBuster: "buster-b"})
	msg5, err := run5.Error().Message(ctx)
	require.NoError(t, err)
	require.NotEqual(t, msg4, msg5)

	boomPattern := regexp.MustCompile(`boom-\d+`)

	_, err = mod.Checks().Run(dagger.CheckGroupRunOpts{CacheBuster: "group-buster"}).List(ctx)
	require.Error(t, err)
	groupMsg1 := boomPattern.FindString(err.Error())
	require.NotEmpty(t, groupMsg1)
}

func (ChecksSuite) TestCheckCacheBusterAcrossClientsOnLongRunningEngine(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	engineSvc, err := c.Host().Tunnel(devEngineContainerAsService(devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { engineSvc.Stop(ctx) })

	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)

	modDir := newFlakyChecksModule(ctx, t)

	connectToRunner := func() *dagger.Client {
		client, err := dagger.Connect(
			ctx,
			dagger.WithRunnerHost(endpoint),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		)
		require.NoError(t, err)
		return client
	}

	loadCheck := func(client *dagger.Client) *dagger.Check {
		mod, err := client.ModuleSource(modDir).AsModule().Sync(ctx)
		require.NoError(t, err)
		return mod.Check("flaky")
	}

	client1 := connectToRunner()
	check1 := loadCheck(client1)
	msg1, err := check1.Run().Error().Message(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msg1)
	require.NoError(t, client1.Close())

	client2 := connectToRunner()
	check2 := loadCheck(client2)
	msg2, err := check2.Run().Error().Message(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msg2)

	msg3, err := check2.Run(dagger.CheckRunOpts{CacheBuster: "buster-a"}).Error().Message(ctx)
	require.NoError(t, err)
	require.NotEqual(t, msg2, msg3)

	msg3Again, err := check2.Run(dagger.CheckRunOpts{CacheBuster: "buster-a"}).Error().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, msg3, msg3Again)
	require.NoError(t, client2.Close())

	client3 := connectToRunner()
	check3 := loadCheck(client3)
	msg4, err := check3.Run().Error().Message(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msg4)

	msg5, err := check3.Run(dagger.CheckRunOpts{CacheBuster: "buster-a"}).Error().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, msg3, msg5)

	msg6, err := check3.Run(dagger.CheckRunOpts{CacheBuster: "buster-b"}).Error().Message(ctx)
	require.NoError(t, err)
	require.NotEqual(t, msg5, msg6)

	msg6Again, err := check3.Run(dagger.CheckRunOpts{CacheBuster: "buster-b"}).Error().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, msg6, msg6Again)
	require.NoError(t, client3.Close())
}

func newFlakyChecksModule(ctx context.Context, t *testctx.T) string {
	t.Helper()

	modDir := t.TempDir()
	initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	initOutput, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(initOutput))

	depDir := filepath.Join(modDir, "dep")
	require.NoError(t, os.Mkdir(depDir, 0o755))
	initDepCmd := hostDaggerCommand(ctx, t, depDir, "init", "--source=.", "--name=dep", "--sdk=go")
	initDepOutput, err := initDepCmd.CombinedOutput()
	require.NoError(t, err, string(initDepOutput))

	require.NoError(t, os.WriteFile(filepath.Join(depDir, "main.go"), []byte(`package main

import (
	"strconv"
	"time"
)

type Dep struct{}

func (m *Dep) Fn(rand string) string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}
`), 0o644))

	installCmd := hostDaggerCommand(ctx, t, modDir, "install", depDir)
	installOutput, err := installCmd.CombinedOutput()
	require.NoError(t, err, string(installOutput))

	require.NoError(t, os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
	"context"
	"fmt"
)

type Test struct{}

// +check
func (m *Test) Flaky(ctx context.Context) error {
	value, err := dag.Dep().Fn(ctx, "ignored")
	if err != nil {
		return err
	}
	return fmt.Errorf("boom-%s", value)
}
`), 0o644))

	return modDir
}
