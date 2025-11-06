package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/engine/distconsts"
)

// Find test suites to run
func (dev *DaggerDev) Test() *Test {
	return &Test{Dagger: dev}
}

type Test struct {
	Dagger *DaggerDev // +private
}

// Run all engine tests
// +cache="session"
func (t *Test) All(
	ctx context.Context,
	// +optional
	failfast bool,
	// +optional
	parallel int,
	// +optional
	timeout string,
	// +optional
	race bool,
	// +optional
	envFile *dagger.Secret,
	// +optional
	testVerbose bool,
) (MyCheckStatus, error) {
	cmd, _, err := t.testCmd(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	_, err = t.test(
		cmd,
		&testOpts{
			runTestRegex:  "",
			skipTestRegex: "",
			pkg:           "./...",
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         1,
			envs:          envFile,
			testVerbose:   testVerbose,
		},
	).Sync(ctx)
	return CheckCompleted, err
}

// Run telemetry tests
// +cache="session"
func (t *Test) Telemetry(
	ctx context.Context,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// +optional
	update bool,
	// +optional
	failfast bool,
	// +optional
	parallel int,
	// +optional
	timeout string,
	// +optional
	race bool,
	// +default=1
	count int,
	// +optional
	envFile *dagger.Secret,
	// +optional
	testVerbose bool,
) (*dagger.Directory, error) {
	cmd, _, err := t.testCmd(ctx)
	if err != nil {
		return nil, err
	}
	ran, err := t.test(
		cmd,
		&testOpts{
			runTestRegex:  run,
			skipTestRegex: skip,
			pkg:           "./dagql/idtui/",
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         count,
			update:        update,
			envs:          envFile,
			testVerbose:   testVerbose,
		},
	).Sync(ctx)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithDirectory(
		"./dagql/idtui/testdata/",
		ran.Directory("./dagql/idtui/testdata/"),
	), nil
}

// List all tests
func (t *Test) List(ctx context.Context) (string, error) {
	// FIXME: don't need a full-blown test environment (with engine sidecar & dagger CLI) just to list tests
	cmd, _, err := t.testCmd(ctx)
	if err != nil {
		return "", err
	}

	return cmd.
		WithExec([]string{"sh", "-c", "go test -list=. ./... | grep ^Test | sort"}).
		Stdout(ctx)
}

// Run specific tests
// +cache="session"
func (t *Test) Specific(
	ctx context.Context,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// +optional
	// +default="./..."
	pkg string,
	// Abort test run on first failure
	// +optional
	failfast bool,
	// How many tests to run in parallel - defaults to the number of CPUs
	// +optional
	parallel int,
	// How long before timing out the test run
	// +optional
	timeout string,
	// +optional
	race bool,
	// +default=1
	// +optional
	count int,
	// +optional
	envFile *dagger.Secret,
	// Enable verbose output
	// +optional
	testVerbose bool,
) (MyCheckStatus, error) {
	cmd, _, err := t.testCmd(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	_, err = t.test(
		cmd,
		&testOpts{
			runTestRegex:  run,
			skipTestRegex: skip,
			pkg:           pkg,
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         count,
			envs:          envFile,
			testVerbose:   testVerbose,
		},
	).Sync(ctx)
	return CheckCompleted, err
}

// Update specific tests
// +cache="session"
func (t *Test) Update(
	ctx context.Context,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// +optional
	// +default="./..."
	pkg string,
	// Abort test run on first failure
	// +optional
	failfast bool,
	// How many tests to run in parallel - defaults to the number of CPUs
	// +optional
	parallel int,
	// How long before timing out the test run
	// +optional
	timeout string,
	// +optional
	race bool,
	// +default=1
	// +optional
	count int,
	// +optional
	envFile *dagger.Secret,
	// Enable verbose output
	// +optional
	testVerbose bool,
) (*dagger.Changeset, error) {
	cmd, _, err := t.testCmd(ctx)
	if err != nil {
		return nil, err
	}
	ran, err := t.test(
		cmd,
		&testOpts{
			runTestRegex:  run,
			skipTestRegex: skip,
			pkg:           pkg,
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         count,
			envs:          envFile,
			testVerbose:   testVerbose,
			update:        true,
		},
	).Sync(ctx)
	if err != nil {
		return nil, err
	}
	return ran.Directory(".").Changes(t.Dagger.Source), nil
}

// Run specific tests while curling (pprof) dumps from their associated dev engine:
// defaults to heap dumps, eg: take a heap dump every second and one after the tests complete:
// `dagger call test dump --run=TestCache/TestVolume --pkg=./core/integration --interval=1s export --path=/tmp/dump-$(date +"%Y%m%d_%H%M%S")`
// but also works for profiles:
// `dagger call test dump --run=TestCache/TestVolume --pkg=./core/integration --route=pprof/profile --no-final export --path=/tmp/dump-$(date +"%Y%m%d_%H%M%S")`
// +cache="session"
func (t *Test) Dump(
	ctx context.Context,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// +optional
	// +default="./..."
	pkg string,
	// Abort test run on first failure
	// +optional
	failfast bool,
	// How many tests to run in parallel - defaults to the number of CPUs
	// +optional
	parallel int,
	// How long before timing out the test run
	// +optional
	timeout string,
	// +optional
	race bool,
	// +default=1
	// +optional
	count int,
	// Enable verbose output
	// +optional
	testVerbose bool,
	// debug subroute to dump, like pprof/profile, pprof/heap, or requests
	// +optional
	// +default="pprof/heap"
	route string,
	// when set, don't take a final dump after the tests have completed. usually good with --route="pprof/profile".
	// +optional'
	// +default=false
	noFinal bool,
	// wait this long before starting to take dumps. delay does not include engine startup.
	// +optional
	// +default="1s"
	delay string,
	// wait this long between dumps. negative values will fetch exactly 1 dump excluding the one controlled by "final"
	// +optional
	// +default="-1s"
	interval string,
) (*dagger.Directory, error) {
	d, err := time.ParseDuration(delay)
	if err != nil {
		return nil, err
	}

	i, err := time.ParseDuration(interval)
	if err != nil {
		return nil, err
	}

	return t.testDump(
		ctx,
		&testOpts{
			runTestRegex:  run,
			skipTestRegex: skip,
			pkg:           pkg,
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         count,
			testVerbose:   testVerbose,
		},
		&dumpOpts{
			route:    route,
			noFinal:  noFinal,
			delay:    d,
			interval: i,
		},
	)
}

type testOpts struct {
	runTestRegex  string
	skipTestRegex string
	pkg           string
	failfast      bool
	parallel      int
	timeout       string
	race          bool
	count         int
	update        bool
	envs          *dagger.Secret
	testVerbose   bool
	bench         bool
}

type dumpOpts struct {
	route    string
	noFinal  bool
	delay    time.Duration
	interval time.Duration
}

func (t *Test) dump(
	ctx context.Context,
	testContainer *dagger.Container,
	debugEndpoint string,
	opts *dumpOpts,
) (*dagger.Directory, error) {
	dumps := dag.Directory()
	baseFileName := strings.ReplaceAll(opts.route, "/", "-")
	dumpCount := 0 // not strictly necessary, but a nice sanity check and less faff than using dumps.Entries()

	cancelCtx, cancel := context.WithCancel(ctx)
	eg := errgroup.Group{}
	wait := opts.delay
	eg.Go(func() error {
		var dumpErr error
		for {
			select {
			case <-cancelCtx.Done():
				return dumpErr
			case <-time.After(wait):
				heapData, err := fetchDump(ctx, debugEndpoint, opts.route)
				dumpErr = errors.Join(dumpErr, err)
				if err == nil {
					fileName := fmt.Sprintf("%s-%d.pprof", baseFileName, dumpCount)
					dumps = dumps.WithFile(fileName, heapData)
					dumpCount++
				}
				if opts.interval < 0 {
					return dumpErr
				}
				wait = opts.interval
			}
		}
	})

	_, testErr := testContainer.Sync(ctx)
	cancel()
	dumpErr := eg.Wait()

	if !opts.noFinal {
		heapData, finalDumpErr := fetchDump(ctx, debugEndpoint, opts.route)
		dumpErr = errors.Join(dumpErr, finalDumpErr)
		if finalDumpErr == nil {
			fileName := fmt.Sprintf("%s-final.pprof", baseFileName)
			dumps = dumps.WithFile(fileName, heapData)
			dumpCount++
		}
	}

	if testErr != nil {
		if dumpCount == 0 {
			return nil, fmt.Errorf("no dumps collected and test failed: %w, %w", testErr, dumpErr)
		}
		return dumps, testErr
	}

	if dumpCount == 0 {
		return nil, fmt.Errorf("test passed, but no dumps collected: %w", dumpErr)
	}
	return dumps, nil
}

func (t *Test) testDump(
	ctx context.Context,
	opts *testOpts,
	dOpts *dumpOpts,
) (*dagger.Directory, error) {
	cmd, debugEndpoint, err := t.testCmd(ctx)
	if err != nil {
		return nil, err
	}

	testContainer := t.test(
		cmd,
		&testOpts{
			runTestRegex:  opts.runTestRegex,
			skipTestRegex: opts.skipTestRegex,
			pkg:           opts.pkg,
			failfast:      opts.failfast,
			parallel:      opts.parallel,
			timeout:       opts.timeout,
			race:          opts.race,
			count:         opts.count,
			update:        false,
			testVerbose:   opts.testVerbose,
			bench:         false,
		},
	)

	return t.dump(
		ctx,
		testContainer,
		debugEndpoint,
		dOpts,
	)
}

// fetchDump fetches from a debug HTTP endpoint and returns it as a dagger.File
func fetchDump(ctx context.Context, debugEndpoint string, route string) (*dagger.File, error) {
	url := fmt.Sprintf("%s/debug/%s", debugEndpoint, route)
	curlContainer := dag.Wolfi().Container(dagger.WolfiContainerOpts{Packages: []string{"curl"}}).
		WithExec([]string{
			"curl",
			"--fail",
			"--silent",
			"--show-error",
			"--max-time", "120", // Timeout after 120 seconds (for longer CPU profiles)
			"--output", "/dump",
			url,
		})

	exitCode, err := curlContainer.ExitCode(ctx)
	if err != nil {
		return nil, err
	}

	if exitCode != 0 {
		stderr, _ := curlContainer.Stderr(ctx)
		return nil, fmt.Errorf("failed to fetch dump, curl exit code: %d, stderr: %s", exitCode, stderr)
	}

	return curlContainer.File("/dump"), nil
}

func (t *Test) test(
	cmd *dagger.Container,
	opts *testOpts,
) *dagger.Container {
	if opts.envs != nil {
		cmd = cmd.WithMountedSecret("/dagger.env", opts.envs)
	}

	cgoEnabledEnv := "0"
	args := []string{
		"go",
		"test",
	}

	// allow verbose
	if opts.testVerbose {
		args = append(args, "-v")
	}

	// Add ldflags
	ldflags := []string{
		"-X", "github.com/dagger/dagger/engine.Version=" + t.Dagger.Version,
		"-X", "github.com/dagger/dagger/engine.Tag=" + t.Dagger.Tag,
	}
	args = append(args, "-ldflags", strings.Join(ldflags, " "))

	// All following are go test flags
	if opts.failfast {
		args = append(args, "-failfast")
	}

	// Go will default parallel to number of CPUs, so only pass if set
	if opts.parallel != 0 {
		args = append(args, fmt.Sprintf("-parallel=%d", opts.parallel))
	}

	// Default timeout to 30m
	// No test suite should take more than 30 minutes to run
	if opts.timeout == "" {
		opts.timeout = "30m"
	}
	args = append(args, fmt.Sprintf("-timeout=%s", opts.timeout))

	if opts.race {
		args = append(args, "-race")
		cgoEnabledEnv = "1"
	}

	// when bench is true, disable normal tests and select benchmarks based on runTestRegex instead
	if opts.bench {
		if opts.runTestRegex == "" {
			opts.runTestRegex = "."
		}
		args = append(args, "-bench", opts.runTestRegex, "-run", "^$")
		args = append(args, fmt.Sprintf("-benchtime=%dx", opts.count))
	} else {
		// Disable test caching, since these are integration tests
		args = append(args, fmt.Sprintf("-count=%d", opts.count))
		if opts.runTestRegex != "" {
			args = append(args, "-run", opts.runTestRegex)
		}
	}

	if opts.skipTestRegex != "" {
		args = append(args, "-skip", opts.skipTestRegex)
	}

	args = append(args, opts.pkg)

	if opts.update {
		args = append(args, "-update")
	}

	return cmd.
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithExec(args)
}

func (t *Test) testCmd(ctx context.Context) (*dagger.Container, string, error) {
	engine := dag.DaggerEngine().
		WithBuildkitConfig(`registry."registry:5000"`, `http = true`).
		WithBuildkitConfig(`registry."privateregistry:5000"`, `http = true`).
		WithBuildkitConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`)
	devEngine := engine.Container()

	// TODO: mitigation for https://github.com/dagger/dagger/issues/8031
	// during our test suite
	devEngine = devEngine.
		WithEnvVariable("_DAGGER_ENGINE_SYSTEMENV_GODEBUG", "goindex=0")

	devBinary := dag.DaggerCli().Binary()
	// This creates an engine.tar container file that can be used by the integration tests.
	// In particular, it is used by core/integration/remotecache_test.go to create a
	// dev engine that can be used to test remote caching.
	// I also load the dagger binary, so that the remote cache tests can use it to
	// run dagger queries.

	// These are used by core/integration/remotecache_test.go
	testEngineUtils := dag.Directory().
		WithFile("engine.tar", devEngine.AsTarball()).
		WithFile("dagger", devBinary, dagger.DirectoryWithFileOpts{
			Permissions: 0755,
		})

	engineRunVol := dag.CacheVolume("dagger-dev-engine-test-varrun" + rand.Text())
	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+rand.Text())).
		WithMountedCache("/run", engineRunVol).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"--addr", "unix:///run/dagger-engine.sock",
				"--addr", "tcp://0.0.0.0:1234",
				"--network-name", "dagger-dev",
				"--network-cidr", "10.88.0.0/16",
				"--debugaddr", "0.0.0.0:6060",
			},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})

	// manually starting service to ensure it's not reaped between benchmark prewarm & run
	devEngineSvc, err := devEngineSvc.Start(ctx)
	if err != nil {
		return nil, "", err
	}

	endpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, "", err
	}

	debugEndpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 6060, Scheme: "http"})
	if err != nil {
		return nil, "", err
	}

	cliBinPath := "/.dagger-cli"

	utilDirPath := "/dagger-dev"
	// FIXME: fold test functions *into* the Go toolchain,
	// instead of calling *out to* it.
	goToolchain, err := t.Dagger.Go(ctx, t.Dagger.Source)
	if err != nil {
		return nil, "", err
	}
	tests := goToolchain.Env().
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithServiceBinding("daggerengine", devEngineSvc).
		WithMountedCache("/run", engineRunVol).
		WithServiceBinding("registry", registrySvc)

	// TODO: should use c.Dagger.installer (but this currently can't connect to services)
	tests = tests.
		WithMountedFile(cliBinPath, devBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		With(t.Dagger.withDockerCfg) // this avoids rate limiting in our ci tests
	return tests, debugEndpoint, nil
}

func registry() *dagger.Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}

func privateRegistry() *dagger.Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return dag.Container().
		From("registry:2").
		WithNewFile("/auth/htpasswd", htpasswd).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}
