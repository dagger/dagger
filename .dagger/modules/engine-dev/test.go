package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dagger/engine-dev/build"
	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
	clidev "dagger/engine-dev/internal/dagger/clients/cli-dev"
	golang "dagger/engine-dev/internal/dagger/clients/go"
	"dagger/engine-dev/internal/dagger/clients/wolfi"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/engine/distconsts"
)

// List all core engine tests
func (dev *EngineDev) Tests(ctx context.Context) (string, error) {
	builder, err := build.NewBuilder(ctx, dev.Source, "", dev.VCSCommit, dev.VCSDirty)
	if err != nil {
		return "", err
	}
	tests, err := builder.Tests(ctx)
	if err != nil {
		return "", err
	}
	var names []string
	for _, pkgTests := range tests {
		for _, name := range pkgTests {
			if strings.HasPrefix(name, "Test") {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return strings.Join(names, "\n") + "\n", nil
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
	failFast bool,
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
	// Enable privileged eBPF tests (Linux 6.15 or newer)
	// +optional
	ebpf bool,
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
	ctr, err := dev.testContainer(ctx, ebpfProgs, false)
	if err != nil {
		return err
	}
	_, err = dev.test(ctx, ctr, &testOpts{
		runTestRegex:  run,
		skipTestRegex: skip,
		pkg:           pkg,
		failFast:      failFast,
		parallel:      parallel,
		timeout:       timeout,
		race:          race,
		count:         count,
		ebpf:          ebpf,
		envs:          envFile,
		testVerbose:   testVerbose,
		update:        update,
		dumpAfter:     dumpTimes,
	})
	return err
}

// The result of a profiled test run: the test engine's wcprof recording, and
// how the tests went.
type TestProfileResult struct {
	// The test engine's wcprof dump (engine/wcprof), covering every
	// session the tests opened
	Dump *dagger.File
	// The exit status of `go test`
	ExitCode int
	// The tail of the test output (stdout and stderr)
	Output string
}

// How many trailing lines of test output a TestProfileResult keeps.
const testProfileOutputLines = 200

// The environment variable that opts benchmark-style integration tests in.
// They are skipped unless it is set, which keeps them out of CI.
const benchTestsEnv = "_DAGGER_BENCH"

// Run core engine tests against an engine recording a wcprof wall-clock
// profile, and return the recording.
//
// The test engine records every session from startup (its --wcprof flag). The
// dump is fetched from its debug endpoint after `go test` exits, whether or
// not the tests passed, so a failing run still yields a profile. Benchmark
// tests gated on _DAGGER_BENCH are opted in, since profiling is what they are
// for: select them with `run` like any other test.
// +cache="session"
func (dev *EngineDev) TestProfile(
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
	failFast bool,
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
	// Enable the given ebpf progs in the engine during tests
	// +optional
	ebpfProgs []string,
) (*TestProfileResult, error) {
	ctr, err := dev.testContainer(ctx, ebpfProgs, true)
	if err != nil {
		return nil, err
	}
	ran, err := dev.test(ctx, ctr.WithEnvVariable(benchTestsEnv, "1"), &testOpts{
		runTestRegex:  run,
		skipTestRegex: skip,
		pkg:           pkg,
		failFast:      failFast,
		parallel:      parallel,
		timeout:       timeout,
		race:          race,
		count:         count,
		envs:          envFile,
		testVerbose:   testVerbose,
		expectAny:     true,
	})
	if err != nil {
		return nil, err
	}
	// The run's exit code is the first package's that failed, and its output
	// every package's, in order.
	var exitCode int
	var outputs []string
	for _, pkgRan := range ran {
		code, err := pkgRan.ExitCode(ctx)
		if err != nil {
			return nil, err
		}
		if exitCode == 0 {
			exitCode = code
		}
		output, err := pkgRan.CombinedOutput(ctx)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	output := strings.Join(outputs, "")
	// The engine service was started explicitly by testContainer, so it is
	// still the same engine (and recording) after the tests exit.
	dump := ran[0].
		WithExec([]string{
			"curl", "--fail", "--silent", "--show-error", "--max-time", "300",
			"-o", "/tmp/wcprof.dump", wcprofDumpURL,
		}).
		File("/tmp/wcprof.dump")
	dump, err = dump.Sync(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch wcprof dump (tests exited %d):\n%s: %w", exitCode, tailLines(output, 40), err)
	}
	return &TestProfileResult{
		Dump:     dump,
		ExitCode: exitCode,
		Output:   tailLines(output, testProfileOutputLines),
	}, nil
}

const wcprofDumpURL = "http://daggerengine:6060/debug/wcprof/dump"

// tailLines keeps the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return fmt.Sprintf("… (%d earlier lines omitted) …\n", len(lines)-n) + strings.Join(lines[len(lines)-n:], "\n")
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
	failFast bool,
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
	// Goldens compare a warmed cache with the following CLI invocation.
	// Keep warmup results available after its session closes.
	dev = dev.WithEngineConfig("worker.oci", "gc = false")
	ctr, err := dev.testContainer(ctx, ebpfProgs, false)
	if err != nil {
		return nil, err
	}
	ran, err := dev.test(ctx, ctr, &testOpts{
		runTestRegex:  run,
		skipTestRegex: skip,
		pkg:           "./dagql/idtui/",
		failFast:      failFast,
		parallel:      parallel,
		timeout:       timeout,
		race:          race,
		count:         count,
		update:        update,
		envs:          envFile,
		testVerbose:   testVerbose,
	})
	if err != nil {
		return nil, err
	}
	return ran[0].Directory(testSourcePath).Changes(ctr.Directory(testSourcePath)), nil
}

type testOpts struct {
	ebpf          bool
	runTestRegex  string
	skipTestRegex string
	pkg           string
	failFast      bool
	parallel      int
	timeout       string
	race          bool
	count         int
	update        bool
	envs          *dagger.Secret
	testVerbose   bool
	bench         bool
	dumpAfter     []time.Duration
	// Don't fail the exec when the tests fail, so the caller can inspect
	// the exit code and still use the container afterwards
	expectAny bool
}

func (dev *EngineDev) test(
	ctx context.Context,
	// The test container to run the tests in
	container *dagger.Container,
	// Various test options
	// FIXME merge this into chainable functions instead
	opts *testOpts,
) ([]*dagger.Container, error) {
	if opts.ebpf {
		container = container.WithEnvVariable("DAGGER_TEST_EBPF", "1")
	}
	if opts.envs != nil {
		container = container.WithMountedSecret("/dagger.env", opts.envs)
	}

	// The test binaries are built by dagger/go's per-package build, as go
	// test -c builds them, and run as go test runs them: each from its
	// package's directory, several packages at once.
	builder, err := build.NewBuilder(ctx, dev.Source, "", dev.VCSCommit, dev.VCSDirty)
	if err != nil {
		return nil, err
	}
	tests, err := builder.Tests(ctx)
	if err != nil {
		return nil, err
	}
	dirs, err := testPackages(tests, strings.Fields(opts.pkg))
	if err != nil {
		return nil, err
	}
	cgoEnabledEnv := "0"
	if opts.race {
		cgoEnabledEnv = "1"
	}

	// The test binaries' own flags, as go test passes them
	var flags []string

	// allow verbose
	if opts.testVerbose {
		flags = append(flags, "-test.v=true")
	}

	if opts.failFast {
		flags = append(flags, "-test.failfast")
	}

	// Go will default parallel to number of CPUs, so only pass if set
	if opts.parallel != 0 {
		flags = append(flags, fmt.Sprintf("-test.parallel=%d", opts.parallel))
	}

	// Default timeout to 20m: Cloud cancels a job at thirty minutes, so a
	// package must time out first to leave a goroutine dump behind.
	if opts.timeout == "" {
		opts.timeout = "20m"
	}
	flags = append(flags, fmt.Sprintf("-test.timeout=%s", opts.timeout))

	// when bench is true, disable normal tests and select benchmarks based on runTestRegex instead
	if opts.bench {
		if opts.runTestRegex == "" {
			opts.runTestRegex = "."
		}
		flags = append(flags, "-test.bench="+opts.runTestRegex, "-test.run=^$")
		flags = append(flags, fmt.Sprintf("-test.benchtime=%dx", opts.count))
	} else {
		// Disable test caching, since these are integration tests
		flags = append(flags, fmt.Sprintf("-test.count=%d", opts.count))
		if opts.runTestRegex != "" {
			flags = append(flags, "-test.run="+opts.runTestRegex)
		}
	}

	if opts.skipTestRegex != "" {
		flags = append(flags, "-test.skip="+opts.skipTestRegex)
	}

	if opts.update {
		flags = append(flags, "-update")
	}

	execOpts := dagger.ContainerWithExecOpts{
		// The test itself loads BPF and creates cgroups. Privileging only
		// the separate dev-engine service does not grant it those rights.
		InsecureRootCapabilities: opts.ebpf,
	}
	if opts.expectAny {
		execOpts.Expect = dagger.ReturnTypeAny
	}
	binaries := make([]*dagger.File, len(dirs))
	runs := make([]*dagger.Container, len(dirs))
	for i, dir := range dirs {
		args := append([]string{"otelgotest", testBinaryPath}, flags...)
		if len(opts.dumpAfter) > 0 {
			watchArgs := []string{"sh", "-c", engineDumpWatchdog, "engine-dump-watchdog"}
			for _, after := range opts.dumpAfter {
				watchArgs = append(watchArgs, fmt.Sprintf("%.9f", after.Seconds()), after.String())
			}
			watchArgs = append(watchArgs, "--")
			args = append(watchArgs, args...)
		}
		binaries[i] = builder.TestBinary(dir, opts.race)
		runs[i] = container.
			WithMountedFile(testBinaryPath, binaries[i]).
			WithWorkdir(path.Join(testSourcePath, dir)).
			WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
			WithExec(withoutOuterSession(args...), execOpts)
	}
	return runTestPackages(ctx, binaries, runs, opts.failFast, opts.expectAny)
}

// runTestPackages runs each package's tests, runs, as soon as its test binary
// is built, up to one package per CPU at a time, as go test runs packages.
// With failFast, as with go test -failfast, no package starts after one has
// failed. With expectAny, a package's tests failing is its nonzero exit code,
// not an error. It returns the containers that ran, in order, and every error.
func runTestPackages(ctx context.Context, binaries []*dagger.File, runs []*dagger.Container, failFast, expectAny bool) ([]*dagger.Container, error) {
	slots := make(chan struct{}, runtime.NumCPU())
	var failed atomic.Bool
	ran := make([]*dagger.Container, len(runs))
	errs := make([]error, len(runs))
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() {
			if _, err := binaries[i].Sync(ctx); err != nil {
				errs[i] = err
				failed.Store(true)
				return
			}
			slots <- struct{}{}
			defer func() { <-slots }()
			if failFast && failed.Load() {
				return
			}
			ctr, err := runs[i].Sync(ctx)
			if err != nil {
				errs[i] = err
				failed.Store(true)
				return
			}
			ran[i] = ctr
			if expectAny {
				code, err := ctr.ExitCode(ctx)
				if err != nil {
					errs[i] = err
				}
				if err != nil || code != 0 {
					failed.Store(true)
				}
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(ran, func(ctr *dagger.Container) bool { return ctr == nil }), nil
}

// Where the test container has the source, and where a package's test binary
// is mounted in it.
const (
	testSourcePath = "/app"
	testBinaryPath = "/dagger-tests/pkg.test"
)

// testPackages are the directories of the source's root Go module whose tests
// patterns select, in order. tests are the test names of every package that
// has any, by directory. A pattern is a package or package pattern as go test
// takes it, relative ("./dir", "./dir/...", "./.../dir") or by import path;
// a pattern starting with "!" removes what it selects. A package that a
// wildcard selects but that has no tests is left out, as go test leaves it
// out; one named exactly must have tests.
func testPackages(tests map[string][]string, patterns []string) ([]string, error) {
	all := make([]string, 0, len(tests))
	for dir := range tests {
		all = append(all, dir)
	}
	slices.Sort(all)
	match := func(pattern string) ([]string, error) {
		pattern = modulePattern(pattern)
		if !strings.Contains(pattern, "...") {
			if _, ok := tests[pattern]; !ok {
				return nil, fmt.Errorf("package %s has no tests", pattern)
			}
			return []string{pattern}, nil
		}
		matches := goPatternMatcher(pattern)
		var dirs []string
		for _, dir := range all {
			if matches(dir) {
				dirs = append(dirs, dir)
			}
		}
		return dirs, nil
	}
	var selected []string
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		dirs, err := match(pattern)
		if err != nil {
			return nil, err
		}
		for _, dir := range dirs {
			if !slices.Contains(selected, dir) {
				selected = append(selected, dir)
			}
		}
	}
	for _, pattern := range patterns {
		if excluded, ok := strings.CutPrefix(pattern, "!"); ok {
			dirs, err := match(excluded)
			if err != nil {
				return nil, err
			}
			selected = slices.DeleteFunc(selected, func(dir string) bool { return slices.Contains(dirs, dir) })
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no tests in %s", strings.Join(patterns, " "))
	}
	return selected, nil
}

// modulePattern is a package pattern made relative to the root of the
// source's root Go module, github.com/dagger/dagger: "." for the module's
// root package, "dir" for a package below it.
func modulePattern(pattern string) string {
	const modulePath = "github.com/dagger/dagger"
	if rest, ok := strings.CutPrefix(pattern, modulePath); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		pattern = "." + rest
	}
	pattern = path.Clean(pattern)
	if pattern != "." {
		pattern = strings.TrimPrefix(pattern, "./")
	}
	return pattern
}

// goPatternMatcher reports whether a directory matches a package pattern as
// the go command matches one (cmd/go's search.MatchPattern): "..." matches
// any string, and a pattern ending in "/..." also matches the directory
// before it.
func goPatternMatcher(pattern string) func(string) bool {
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\.\.\.`, `.*`)
	if prefix, ok := strings.CutSuffix(re, `/.*`); ok {
		re = prefix + `(/.*)?`
	}
	return regexp.MustCompile(`^` + re + `$`).MatchString
}

// Use direct HTTP from the runner: asking the engine to execute a dump command
// would itself need the cache locks we may be trying to diagnose. Each timer is
// relative to runner startup, independent of earlier requests, and both the
// timer and an in-flight request are reaped when the tests finish.
//
//nolint:gosec // G101: this constant is a shell script, not a credential.
const engineDumpWatchdog = `
engine_url='http://daggerengine:6060/debug/pprof/goroutine?debug=2'
runner=
watchers=
spawning=false
exit_status=
request_exit() {
  if "$spawning"; then
    exit_status=$1
  else
    exit "$1"
  fi
}
finish_spawn() {
  spawning=false
  if [ -n "$exit_status" ]; then exit "$exit_status"; fi
}
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
trap 'request_exit 129' HUP
trap 'request_exit 130' INT
trap 'request_exit 143' TERM

watch_dump() {
  child=
  spawning=false
  exit_status=
  trap 'if [ -n "$child" ]; then kill -TERM "$child" 2>/dev/null || :; wait "$child" 2>/dev/null || :; fi' EXIT
  trap 'request_exit 0' HUP INT TERM
  start_child() {
    # A signal may arrive between spawning the child and recording its PID.
    # Defer exit until the EXIT trap can kill and reap that child.
    spawning=true
    "$@" &
    child=$!
    finish_spawn
  }
  start_child sleep "$1"
  wait "$child" || return
  child=
  printf '\n=== BEGIN engine goroutine dump after %s: %s ===\n' "$2" "$engine_url" >&2
  start_child curl --fail --silent --show-error --connect-timeout 5 --max-time 30 "$engine_url" >&2
  if wait "$child"; then
    result=0
  else
    result=$?
  fi
  child=
  printf '\n=== END engine goroutine dump after %s: %s (curl exit %s) ===\n' "$2" "$engine_url" "$result" >&2
}

while [ "$1" != "--" ]; do
  spawning=true
  watch_dump "$1" "$2" &
  watchers="$watchers $!"
  finish_spawn
  shift 2
done
shift
spawning=true
"$@" &
runner=$!
finish_spawn
wait "$runner"
result=$?
runner=
exit "$result"
`

// Build an ephemeral test environment ready to run core engine tests.
// With wcprof, the test engine records a wcprof wall-clock profile of all its
// work from startup, readable at http://daggerengine:6060/debug/wcprof/dump.
func (dev *EngineDev) testContainer(ctx context.Context, ebpfProgs []string, wcprof bool) (*dagger.Container, error) {
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
		return nil, err
	}

	// TODO: mitigation for https://github.com/dagger/dagger/issues/8031
	// during our test suite
	devEngine = devEngine.
		WithEnvVariable("_DAGGER_ENGINE_SYSTEMENV_GODEBUG", "goindex=0")
	devEnginePlatform, err := devEngine.Platform(ctx)
	if err != nil {
		return nil, err
	}

	devBinary := clidev.New(clidev.CliDevOpts{VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).Binary()
	// This creates an engine.tar container file that can be used by the integration tests.
	// In particular, it is used by core/integration/remotecache_test.go to create a
	// dev engine that can be used to test remote caching.
	// I also load the dagger binary, so that the remote cache tests can use it to
	// run dagger queries.

	// These are used by core/integration/remotecache_test.go
	testEngineUtils := core.NewDirectory().
		WithFile("engine.tar", devEngine.AsTarball()).
		WithFile("dagger", devBinary, dagger.DirectoryWithFileOpts{
			Permissions: 0o755,
		})

	engineArgs := []string{
		"--addr", "unix:///run/dagger-engine.sock",
		"--addr", "tcp://0.0.0.0:1234",
		"--network-name", "dagger-dev",
		"--network-cidr", "10.88.0.0/16",
		"--debugaddr", "0.0.0.0:6060",
	}
	if wcprof {
		// Record all of the engine's work from startup.
		engineArgs = append(engineArgs, "--wcprof")
	}

	engineRunVol := core.NewCacheVolume("dagger-dev-engine-test-varrun" + rand.Text())
	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithExposedPort(6060, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, core.NewCacheVolume("dagger-dev-engine-test-state"+rand.Text())).
		WithMountedCache("/run", engineRunVol).
		AsService(dagger.ContainerAsServiceOpts{
			Args:                     engineArgs,
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})

	// manually starting service to ensure it's not reaped between benchmark prewarm & run
	// FIXME: just persist the dev engine into a field of the object... cleaner
	devEngineSvc, err = devEngineSvc.Start(ctx)
	if err != nil {
		return nil, err
	}

	builder, err := build.NewBuilder(ctx, dev.Source, "", dev.VCSCommit, dev.VCSDirty)
	if err != nil {
		return nil, err
	}
	otelgotest, err := builder.Otelgotest(ctx)
	if err != nil {
		return nil, err
	}
	utilDirPath := "/dagger-dev"
	tests := dev.GoEnv(dev.Source, false).
		// Tests that compile parts of this repository in the dev engine seed
		// its module cache from this one (withRepoGoModules in
		// core/integration), so it holds the repository's dependencies, as it
		// did when the tests were compiled here.
		WithExec([]string{"go", "mod", "download"}).
		WithFile("/usr/local/bin/otelgotest", otelgotest).
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_PLATFORM", string(devEnginePlatform)).
		WithServiceBinding("daggerengine", devEngineSvc).
		WithMountedCache("/run", engineRunVol).
		WithServiceBinding("registry", registrySvc)

	return dev.InstallClient(ctx, tests, devEngineSvc)
}

func registry() *dagger.Service {
	return core.NewContainer().
		From("registry:2").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}

func privateRegistry() *dagger.Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return core.NewContainer().
		From("registry:2").
		WithNewFile("/auth/htpasswd", htpasswd).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})
}

// GoEnv is a Go environment for working on dagger/dagger: wolfi with the Go
// toolchain, a C toolchain, and what the engine's tests use, with dagger/go's
// Go caches, and source as its working directory. It is for running Go code
// and the go command; dagger/dagger's own binaries and test binaries are
// built with dagger/go's per-package build, not here.
func (dev *EngineDev) GoEnv(
	// The source to work in; the engine's source by default
	// +optional
	source *dagger.Directory,
	// Enable cgo
	// +optional
	cgo bool,
) *dagger.Container {
	if source == nil {
		source = dev.Source
	}
	base := wolfi.New().
		Container(wolfi.WolfiContainerOpts{Packages: []string{
			"go-" + consts.GolangVersion,
			"ca-certificates",
			// a C toolchain, for cgo and the race detector
			"build-base",
			"git",
			// protoc, at the version dagger/dagger's own protobuf code needs
			"protobuf~35",
			"protobuf-dev~35",
			// the engine's tests exercise real client-managed agents and
			// encrypted key loading
			"openssh-client",
			"curl",
		}}).
		WithEnvVariable("GOPATH", "/go").
		WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", dagger.ContainerWithEnvVariableOpts{Expand: true})
	cgoEnabled := "0"
	if cgo {
		cgoEnabled = "1"
	}
	ws := source.AsWorkspace()
	return golang.New(golang.GoOpts{Base: base}).
		Module(ws, ".").
		Base(ws).
		// Go's module index keys imports by path, size and mtime, so normalized
		// Git trees can alias across builds sharing GOCACHE. Keep the compiled
		// artifact cache, but read imports from source (dagger/dagger#8031).
		WithEnvVariable("GODEBUG", "${GODEBUG},goindex=0", dagger.ContainerWithEnvVariableOpts{Expand: true}).
		WithEnvVariable("CGO_ENABLED", cgoEnabled).
		WithMountedDirectory(testSourcePath, source).
		WithWorkdir(testSourcePath)
}
