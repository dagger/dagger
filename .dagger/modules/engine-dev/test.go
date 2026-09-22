package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"dagger/engine-dev/internal/dagger"

	"github.com/dagger/dagger/engine/distconsts"
)

// List all core engine tests
func (dev *EngineDev) Tests(ctx context.Context) (string, error) {
	return dag.Go(dagger.GoOpts{Source: dev.Source, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).Tests(ctx)
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
	// Elapsed times after the test runner starts at which to dump engine goroutines
	// +optional
	dumpAfter []string,
) error {
	dumpTimes := make([]time.Duration, len(dumpAfter))
	for i, after := range dumpAfter {
		duration, err := time.ParseDuration(after)
		if err != nil || duration <= 0 {
			return fmt.Errorf("dumpAfter must contain positive durations: %q", after)
		}
		dumpTimes[i] = duration
	}
	// FIXME: use the damn standard Go toolchain
	ctr, ldflagValues, err := dev.testContainer(ctx, ebpfProgs)
	if err != nil {
		return err
	}
	_, err = dev.test(ctr, &testOpts{
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
		ldflagValues:  ldflagValues,
		dumpAfter:     dumpTimes,
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
) (*dagger.Changeset, error) {
	ctr, ldflagValues, err := dev.testContainer(ctx, ebpfProgs)
	if err != nil {
		return nil, err
	}
	ran, err := dev.test(ctr, &testOpts{
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
		ldflagValues:  ldflagValues,
	},
	).Sync(ctx)
	if err != nil {
		return nil, err
	}
	return ran.Directory(".").Changes(ctr.Directory(".")), nil
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
	ldflagValues  []string
	dumpAfter     []time.Duration
}

func (dev *EngineDev) test(
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
		"otelgotest",
	}

	// allow verbose
	if opts.testVerbose {
		args = append(args, "-v")
	}

	// All following are go test flags
	if opts.failfast {
		args = append(args, "-failfast")
	}

	// Go will default parallel to number of CPUs, so only pass if set
	if opts.parallel != 0 {
		args = append(args, fmt.Sprintf("-parallel=%d", opts.parallel))
	}

	// Default timeout to 20m: Cloud cancels a job at thirty minutes, so a
	// package must time out first to leave a goroutine dump behind.
	if opts.timeout == "" {
		opts.timeout = "20m"
	}
	args = append(args, fmt.Sprintf("-timeout=%s", opts.timeout))

	if opts.race {
		args = append(args, "-race")
		cgoEnabledEnv = "1"
	}
	if len(opts.ldflagValues) > 0 {
		args = append(args, "-ldflags", testLdflags(opts.ldflagValues))
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

	if len(opts.dumpAfter) > 0 {
		watchArgs := []string{"sh", "-c", engineDumpWatchdog, "engine-dump-watchdog"}
		for _, after := range opts.dumpAfter {
			watchArgs = append(watchArgs, fmt.Sprintf("%.9f", after.Seconds()), after.String())
		}
		watchArgs = append(watchArgs, "--")
		args = append(watchArgs, args...)
	}

	return container.
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithExec(args)
}

// Use direct HTTP from the runner: asking the engine to execute a dump command
// would itself need the cache locks we may be trying to diagnose. Each timer is
// relative to runner startup, independent of earlier requests, and both the
// timer and an in-flight request are reaped when the tests finish.
const engineDumpWatchdog = `
engine_url='http://daggerengine:6060/debug/pprof/goroutine?debug=2'
runner=
watchers=
cleanup() {
  trap - EXIT
  if [ -n "$runner" ]; then
    kill -TERM "$runner" 2>/dev/null || :
  fi
  for pid in $watchers; do
    kill -TERM "$pid" 2>/dev/null || :
  done
  wait
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

watch_dump() {
  child=
  trap 'if [ -n "$child" ]; then kill -TERM "$child" 2>/dev/null || :; wait "$child" 2>/dev/null || :; fi' EXIT
  trap 'exit 0' HUP INT TERM
  sleep "$1" &
  child=$!
  wait "$child" || return
  child=
  printf '\n=== BEGIN engine goroutine dump after %s: %s ===\n' "$2" "$engine_url" >&2
  curl --fail --silent --show-error --connect-timeout 5 --max-time 30 "$engine_url" >&2 &
  child=$!
  if wait "$child"; then
    result=0
  else
    result=$?
  fi
  child=
  printf '\n=== END engine goroutine dump after %s: %s (curl exit %s) ===\n' "$2" "$engine_url" "$result" >&2
}

while [ "$1" != "--" ]; do
  watch_dump "$1" "$2" &
  watchers="$watchers $!"
  shift 2
done
shift
"$@" &
runner=$!
wait "$runner"
result=$?
runner=
exit "$result"
`

// Build an ephemeral test environment ready to run core engine tests.
// (FIXME: do this more cleanly, and reuse the standard Go toolchain)
func (dev *EngineDev) testContainer(ctx context.Context, ebpfProgs []string) (*dagger.Container, []string, error) {
	devEngine, err := dev.
		WithEBPFProgs(ebpfProgs).
		WithEngineConfig(`registry."registry:5000"`, `http = true`).
		WithEngineConfig(`registry."privateregistry:5000"`, `http = true`).
		WithEngineConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		Container(
			ctx,
			"",    // platform
			false, // gpuSupport
			"",    // version
		)
	if err != nil {
		return nil, nil, err
	}

	// TODO: mitigation for https://github.com/dagger/dagger/issues/8031
	// during our test suite
	devEngine = devEngine.
		WithEnvVariable("_DAGGER_ENGINE_SYSTEMENV_GODEBUG", "goindex=0")
	devEnginePlatform, err := devEngine.Platform(ctx)
	if err != nil {
		return nil, nil, err
	}

	devBinary := dag.DaggerCli(dagger.DaggerCliOpts{VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).Binary()
	// This creates an engine.tar container file that can be used by the integration tests.
	// In particular, it is used by core/integration/remotecache_test.go to create a
	// dev engine that can be used to test remote caching.
	// I also load the dagger binary, so that the remote cache tests can use it to
	// run dagger queries.

	// These are used by core/integration/remotecache_test.go
	testEngineUtils := dag.Directory().
		WithFile("engine.tar", devEngine.AsTarball()).
		WithFile("dagger", devBinary, dagger.DirectoryWithFileOpts{
			Permissions: 0o755,
		})

	engineRunVol := dag.CacheVolume("dagger-dev-engine-test-varrun" + rand.Text())
	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithExposedPort(6060, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
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
		return nil, nil, err
	}

	utilDirPath := "/dagger-dev"
	goToolchain := dag.Go(dagger.GoOpts{Source: dev.Source, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws,
		// Exercise real client-managed agents and encrypted key loading.
		ExtraPackages: []string{"openssh-client", "curl"},
	})
	ldflagValues, err := goToolchain.Values(ctx)
	if err != nil {
		return nil, nil, err
	}
	tests := goToolchain.Env().
		WithExec([]string{"go", "install", "github.com/dagger/otel-go/cmd/otelgotest"}).
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_PLATFORM", string(devEnginePlatform)).
		WithServiceBinding("daggerengine", devEngineSvc).
		WithMountedCache("/run", engineRunVol).
		WithServiceBinding("registry", registrySvc)

	tests, err = dev.InstallClient(ctx, tests, devEngineSvc)
	if err != nil {
		return nil, nil, err
	}
	return tests, ldflagValues, nil
}

func testLdflags(values []string) string {
	ldflags := make([]string, 0, len(values))
	for _, val := range values {
		ldflags = append(ldflags, "-X '"+val+"'")
	}
	return strings.Join(ldflags, " ")
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
