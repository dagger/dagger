package e2e

import (
	"testing"

	engineDev "github.com/dagger/dagger/sdk/go/e2e/internal/dagger/engine-dev"
)

const bootstrapArchiveName = "dagger-bootstrap.tar.gz"

// TestBootstrap verifies the development client library's CLI bootstrap path.
// The outer harness builds and serves a development CLI, supplies a development
// engine, then runs an isolated inner test importing the development library.
// The inner test receives neither an inherited Dagger session nor a local CLI
// override, so the library must download, verify, cache, and execute the served
// CLI before querying the supplied engine.
//
// Supplying an already-running engine is intentional. Pulling or starting an
// engine through Docker, Podman, containerd, or another runner is CLI bootstrap
// and remains covered by core/integration/provision_test.go.
func TestBootstrap(t *testing.T) {
	h := newHarness(t)
	engine, engineEndpoint := h.startDevEngine(t, "go-client-bootstrap")

	// What the server hands out is the client downloader's input contract, not a
	// production release archive contract: one executable plus a matching
	// checksum entry.
	assets := h.dag.Container().
		From("busybox:1.37").
		WithFile("/srv/dagger", h.devCLIBinary(), engineDev.ContainerWithFileOpts{Permissions: 0o755}).
		WithWorkdir("/srv").
		WithExec([]string{"tar", "czf", bootstrapArchiveName, "dagger"}).
		WithExec([]string{"sh", "-ec", "sha256sum " + bootstrapArchiveName + " > checksums.txt"}).
		WithExposedPort(8080).
		AsService(engineDev.ContainerAsServiceOpts{
			Args: []string{"/bin/httpd", "-f", "-p", "8080", "-h", "/srv"},
		})

	innerSource := h.dag.CurrentWorkspace().Directory("/sdk/go/e2e/testdata/bootstrap")
	devSDKSource := h.dag.CurrentWorkspace().Directory("/sdk/go")

	target := h.dag.Container().
		From("golang:1.26-alpine").
		WithExec([]string{"apk", "add", "--no-cache", "ca-certificates", "coreutils"}).
		WithServiceBinding("bootstrap-assets", assets).
		WithServiceBinding("dagger-engine", engine).
		WithEnvVariable("XDG_CACHE_HOME", "/tmp/dagger-bootstrap-cache").
		WithEnvVariable("_INTERNAL_DAGGER_TEST_CLI_URL", "http://bootstrap-assets:8080/"+bootstrapArchiveName).
		WithEnvVariable("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL", "http://bootstrap-assets:8080/checksums.txt").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
		WithoutEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN").
		WithoutEnvVariable("DAGGER_SESSION_PORT").
		WithoutEnvVariable("DAGGER_SESSION_TOKEN").
		WithDirectory("/sdk", devSDKSource).
		WithDirectory("/work", innerSource).
		WithNewFile("/work/go.mod", `module dagger-bootstrap-inner

go 1.26.1

require dagger.io/dagger v0.0.0

replace dagger.io/dagger => /sdk
`).
		WithFile("/work/go.sum", devSDKSource.File("go.sum")).
		WithWorkdir("/work")

	innerTest := target.WithExec(
		[]string{"timeout", "-k", "10s", "120s", "go", "test", "-v", "-count=1", "-mod=mod", "."},
		engineDev.ContainerWithExecOpts{Expect: engineDev.ReturnTypeAny, NoInit: true},
	)
	requireTargetExec(t, innerTest, "run the development client-library bootstrap tests")
}
