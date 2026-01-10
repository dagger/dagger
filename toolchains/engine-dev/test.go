package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"

	"dagger/engine-dev/internal/dagger"

	"github.com/dagger/dagger/engine/distconsts"
)

// List all core engine tests
func (dev *EngineDev) Tests(ctx context.Context) (string, error) {
	return dag.Go(dagger.GoOpts{Source: dev.sourceWithEbpfObjects()}).Tests(ctx)
}

// Run core engine tests
// +cache="session"
func (dev *EngineDev) Test(
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
	// Update golden files
	// +optional
	update bool,
	// Enable the given ebpf progs in the engine during tests
	// +optional
	ebpfProgs []string,
) error {
	// FIXME: use the damn standard Go toolchain
	ctr, _, err := dev.testContainer(ctx, ebpfProgs)
	if err != nil {
		return err
	}
	_, err = dev.test(ctx, ctr, &testOpts{
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
		update:        update,
	},
	).Sync(ctx)
	return err
}

// Run telemetry tests
// +cache="session"
func (dev *EngineDev) TestTelemetry(
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
	// Enable the given ebpf progs in the engine during tests
	// +optional
	ebpfProgs []string,
) (*dagger.Directory, error) {
	ctr, _, err := dev.testContainer(ctx, ebpfProgs)
	if err != nil {
		return nil, err
	}
	ran, err := dev.test(ctx, ctr, &testOpts{
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

func (dev *EngineDev) test(
	ctx context.Context,
	// The test container to run the tests in
	container *dagger.Container,
	// Various test options
	// FIXME merge this into chainable functions instead
	opts *testOpts,
) *dagger.Container {
	if opts.envs != nil {
		container = container.WithMountedSecret("/dagger.env", opts.envs)
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
	version, err := dag.Version().Version(ctx)
	if err != nil {
		return dag.Container().WithError(err.Error())
	}
	tag, err := dag.Version().ImageTag(ctx)
	if err != nil {
		return dag.Container().WithError(err.Error())
	}
	ldflags := []string{
		"-X", "github.com/dagger/dagger/engine.Version=" + version,
		"-X", "github.com/dagger/dagger/engine.Tag=" + tag,
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

	return container.
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithExec(args)
}

// Build an ephemeral test environment ready to run core engine tests
// Also return the URL of a pprof debug endpoint, to dump profiling data from the tested engine
// (FIXME: do this more cleanly, and reuse the standard Go toolchain)
func (dev *EngineDev) testContainer(ctx context.Context, ebpfProgs []string) (*dagger.Container, string, error) {
	devEngine, err := dev.
		WithEBPFProgs(ebpfProgs).
		WithBuildkitConfig(`registry."registry:5000"`, `http = true`).
		WithBuildkitConfig(`registry."privateregistry:5000"`, `http = true`).
		WithBuildkitConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		Container(
			ctx,
			"",    // platform
			false, // gpuSupport
			"",    // version
			"",    // tag
		)
	if err != nil {
		return nil, "", err
	}

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
	// FIXME: just persist the dev engine into a field of the object... cleaner
	devEngineSvc, err = devEngineSvc.Start(ctx)
	if err != nil {
		return nil, "", err
	}

	debugEndpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 6060, Scheme: "http"})
	if err != nil {
		return nil, "", err
	}

	utilDirPath := "/dagger-dev"
	tests := dag.Go(dagger.GoOpts{Source: dev.sourceWithEbpfObjects()}).Env().
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithServiceBinding("daggerengine", devEngineSvc).
		WithMountedCache("/run", engineRunVol).
		WithServiceBinding("registry", registrySvc)

	tests, err = dev.InstallClient(ctx, tests, devEngineSvc)
	if err != nil {
		return nil, "", err
	}
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
