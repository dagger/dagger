package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/moby/buildkit/identity"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/engine/distconsts"
)

type Test struct {
	Dagger *DaggerDev // +private

	CacheConfig string // +private
}

func (t *Test) WithCache(config string) *Test {
	clone := *t
	clone.CacheConfig = config
	return &clone
}

// Run all engine tests
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
	testVerbose bool,
) error {
	return t.test(
		ctx,
		&testOpts{
			runTestRegex:  "",
			skipTestRegex: "",
			pkg:           "./...",
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         1,
			testVerbose:   testVerbose,
		},
	)
}

// Run telemetry tests
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
	verbose bool,
) (*dagger.Directory, error) {
	engine := t.Dagger.Engine().
		WithConfig(`registry."registry:5000"`, `http = true`).
		WithConfig(`registry."privateregistry:5000"`, `http = true`).
		WithConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		WithConfig(`grpc`, `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`).
		WithArg(`debugaddr`, `0.0.0.0:6060`)
	devEngine, err := engine.Container(ctx, "", nil, false)
	if err != nil {
		return nil, err
	}

	devBinary := dag.DaggerCli().Binary()
	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+identity.NewID())).
		AsService(dagger.ContainerAsServiceOpts{
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})

	endpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	// installed into $PATH
	cliBinPath := "/usr/local/bin/dagger"

	tests := t.Dagger.Go().Env().
		WithServiceBinding("dagger-engine", devEngineSvc).
		WithServiceBinding("registry", registrySvc)

	if t.CacheConfig != "" {
		tests = tests.WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", t.CacheConfig)
	}

	tests = tests.
		WithMountedFile(cliBinPath, devBinary).
		WithEnvVariable("PATH", "/usr/local/bin:${PATH}", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		}).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint)
	if t.Dagger.DockerCfg != nil {
		// this avoids rate limiting in our ci tests
		tests = tests.WithMountedSecret("/root/.docker/config.json", t.Dagger.DockerCfg)
	}

	ran := t.goTest(
		tests,
		&goTestOpts{
			runTestRegex:  run,
			skipTestRegex: skip,
			pkg:           "./dagql/idtui/",
			failfast:      failfast,
			parallel:      parallel,
			timeout:       timeout,
			race:          race,
			count:         count,
			update:        update,
			testVerbose:   verbose,
			bench:         false,
		},
	)
	ran, err = ran.Sync(ctx)
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
	cmd, err := t.testCmd(ctx)
	if err != nil {
		return "", err
	}

	return cmd.
		WithExec([]string{"sh", "-c", "go test -list=. ./... | grep ^Test | sort"}).
		Stdout(ctx)
}

// Run specific tests
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
	// Enable verbose output
	// +optional
	testVerbose bool,
) error {
	return t.test(
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
	testVerbose   bool
}

func (t *Test) test(
	ctx context.Context,
	opts *testOpts,
) error {
	cmd, err := t.testCmd(ctx)
	if err != nil {
		return err
	}

	ctr, err := t.goTest(
		cmd,
		&goTestOpts{
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
	).Sync(ctx)
	if err != nil {
		return err
	}

	// if publish test results is enabled, we pass the expected code
	// dagger.ReturnTypeAny to withExec when running the tests
	// to allow publishing results even if the test execution failed.
	if t.publishTestResultsEnabled() {
		exitCode, err := ctr.ExitCode(ctx)
		if err != nil {
			return fmt.Errorf("get exit code: %w", err)
		}

		stderr, err := ctr.Stderr(ctx)
		if err != nil {
			return fmt.Errorf("get stderr: %w", err)
		}

		_ = t.publishTestResults(ctx, ctr)

		if exitCode != 0 {
			return &dagger.ExecError{
				ExitCode: exitCode,
				Stderr:   stderr,
			}
		}
	}

	return nil
}

func (t *Test) publishTestResults(ctx context.Context, ctr *dagger.Container) error {
	raw, err := t.Dagger.GithubEventFile.Contents(ctx)
	if err != nil {
		return err
	}

	payload := struct {
		Repository string `json:"repository"`
		Event      struct {
			CommitSha string `json:"after"`
		} `json:"event"`
		Job       string `json:"job"`
		Workflow  string `json:"workflow"`
		RunID     string `json:"run_id"`
		RunNumber string `json:"run_number"`
		Branch    string `json:"head_ref"`
	}{}

	err = json.Unmarshal([]byte(raw), &payload)
	if err != nil {
		return err
	}

	metadata := struct {
		RunID     string            `json:"runId" db:"run_id"`
		Repo      string            `json:"repo" db:"repo"`
		Branch    string            `json:"branch" db:"branch"`
		CommitSha string            `json:"commitSha" db:"commit_sha"`
		JobName   string            `json:"jobName" db:"job_name"`
		Format    string            `json:"format" db:"format"`
		Link      string            `json:"link" db:"link"`
		Tags      map[string]string `json:"tags" db:"tags"`
		CreatedAt string            `json:"createdAt" db:"created_at"`
	}{
		RunID:     uuid.NewString(), // a unique identifier per run
		Repo:      "github.com/" + payload.Repository,
		Branch:    payload.Branch,
		CommitSha: payload.Event.CommitSha,
		JobName:   payload.Workflow,
		Format:    "gojson",
		Link:      "https://github.com/dagger/dagger/actions/runs/" + payload.RunID,
		Tags: map[string]string{
			"workflow": payload.Workflow,
			"job":      payload.Job,
		},
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	for _, tag := range t.Dagger.Tags {
		var parts = strings.Split(tag, "=")

		if len(parts) > 1 {
			metadata.Tags[parts[0]] = parts[1]
		} else {
			metadata.Tags[parts[0]] = ""
		}
	}

	metadatajson, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	_, err = ctr.WithNewFile("/publish.sh", fmt.Sprintf(`
#!/bin/sh

curl -vXPOST http://tests-dashboard.x1.ci.dagger.cloud/api/run/%s \
 	-H "Content-Type: multipart/mixed" \
 	-F "results=@/tmp/testresults.json" \
 	-F 'metadata=%s;type=application/json'
`, metadata.RunID, string(metadatajson)), dagger.ContainerWithNewFileOpts{Permissions: 0755}).
		WithExec([]string{"sh", "-c", "/publish.sh"}).
		Sync(ctx)

	return err
}

type goTestOpts struct {
	runTestRegex  string
	skipTestRegex string
	pkg           string
	failfast      bool
	parallel      int
	timeout       string
	race          bool
	count         int
	update        bool
	testVerbose   bool
	bench         bool
}

func (t *Test) goTest(
	cmd *dagger.Container,
	opts *goTestOpts,
) *dagger.Container {
	cgoEnabledEnv := "0"
	args := []string{
		"gotestsum",
		"--format=testname",
		"--jsonfile=/tmp/testresults.json",
		"--",
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

	expect := dagger.ReturnTypeSuccess
	// if publish test results is enabled, don't fail the withExec function
	// when the tests failed to allow publishing the results
	if t.publishTestResultsEnabled() {
		expect = dagger.ReturnTypeAny
	}

	return cmd.
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithExec(args, dagger.ContainerWithExecOpts{Expect: expect})
}

// if running in GitHub context, then publish the test results
func (t *Test) publishTestResultsEnabled() bool {
	return t.Dagger.GithubEventFile != nil
}

func (t *Test) testCmd(ctx context.Context) (*dagger.Container, error) {
	engine := t.Dagger.Engine().
		WithConfig(`registry."registry:5000"`, `http = true`).
		WithConfig(`registry."privateregistry:5000"`, `http = true`).
		WithConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		WithConfig(`grpc`, `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`).
		WithArg(`debugaddr`, `0.0.0.0:6060`)
	devEngine, err := engine.Container(ctx, "", nil, false)
	if err != nil {
		return nil, err
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

	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+identity.NewID())).
		AsService(dagger.ContainerAsServiceOpts{
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})

	// manually starting service to ensure it's not reaped between benchmark prewarm & run
	devEngineSvc, err = devEngineSvc.Start(ctx)
	if err != nil {
		return nil, err
	}

	endpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinPath := "/.dagger-cli"

	utilDirPath := "/dagger-dev"
	tests := t.Dagger.Go().Env().
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithServiceBinding("dagger-engine", devEngineSvc).
		WithServiceBinding("registry", registrySvc)

	if t.CacheConfig != "" {
		tests = tests.WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", t.CacheConfig)
	}

	// TODO: should use c.Dagger.installer (but this currently can't connect to services)
	tests = tests.
		WithMountedFile(cliBinPath, devBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		With(t.Dagger.withDockerCfg) // this avoids rate limiting in our ci tests

	return tests, nil
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
