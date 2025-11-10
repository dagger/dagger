package main

import (
	"context"
	"fmt"
	"runtime"

	"dagger/python-sdk-dev/internal/dagger"
)

// Run the test suite.
type TestSuite struct {
	// The base container to run the tests
	// +private
	Container *dagger.Container
	// The python version to test against
	// +private
	Version string
	// Disable nested execution for the test runs
	// +private
	DisableNestedExec bool
}

// Run the pytest command.
func (t *TestSuite) Run(
	// Arguments to pass to pytest
	args []string,
) *dagger.Container {
	cmd := []string{"uv", "run"}
	if t.Version != "" {
		cmd = append(cmd, "-p", t.Version)
	}
	return t.Container.
		WithExec(
			append(append(cmd, "pytest"), args...),
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true})
}

// Run python tests.
func (t *TestSuite) Default() *dagger.Container {
	return t.Run([]string{"-Wd", "-l", "-m", "not provision"})
}

// Run unit tests.
func (t *TestSuite) Unit() *dagger.Container {
	return t.Run([]string{"-m", "not slow and not provision"})
}

// Test provisioning.
//
// This publishes a cli binary in an ephemeral http server and checks
// if the SDK can download, extract and run it.
func (t *TestSuite) Provision(
	ctx context.Context,
	// Dagger binary to use for test
	cliBin *dagger.File,
	// _EXPERIMENTAL_DAGGER_RUNNER_HOST value
	// +optional
	runnerHost string,
) (*dagger.Container, error) {
	archiveName := fmt.Sprintf("dagger_v0.x.y_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksumsName := "checksums.txt"

	httpServer := t.Container.
		WithMountedFile("/src/dagger", cliBin).
		WithWorkdir("/work").
		WithExec([]string{"tar", "cvzf", archiveName, "-C", "/src", "dagger"}).
		WithExec(
			[]string{"sha256sum", archiveName},
			dagger.ContainerWithExecOpts{RedirectStdout: checksumsName}).
		WithExec([]string{"python", "-m", "http.server"}).
		WithExposedPort(8000).
		AsService()

	httpServerURL, err := httpServer.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "http"})
	if err != nil {
		return nil, err
	}
	archiveURL := fmt.Sprintf("%s/%s", httpServerURL, archiveName)
	checksumsURL := fmt.Sprintf("%s/%s", archiveURL, checksumsName)

	dockerVersion := "24.0.7"

	ctr := dag.Dockerd().Attach(
		t.Container.WithMountedFile(
			"/opt/docker.tgz",
			dag.HTTP(fmt.Sprintf("https://download.docker.com/linux/static/stable/%s/docker-%s.tgz", runtime.GOARCH, dockerVersion)),
			dagger.ContainerWithMountedFileOpts{Owner: "root"}).
			WithExec([]string{
				"tar",
				"xzvf",
				"/opt/docker.tgz",
				"--strip-components=1",
				"-C",
				"/usr/local/bin",
				"docker/docker",
			}),
		dagger.DockerdAttachOpts{DockerVersion: dockerVersion})

	if runnerHost != "" {
		ctr = ctr.WithEnvVariable(
			"_EXPERIMENTAL_DAGGER_RUNNER_HOST",
			runnerHost)
	}

	return ctr.
			WithServiceBinding("http_server", httpServer).
			WithEnvVariable("_INTERNAL_DAGGER_TEST_CLI_URL", archiveURL).
			WithEnvVariable("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL", checksumsURL).
			WithExec(
				[]string{"pytest", "-m", "provision"},
				dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}),
		nil
}
