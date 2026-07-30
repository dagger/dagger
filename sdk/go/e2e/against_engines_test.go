package e2e

import (
	"testing"

	"dagger.io/dagger"
)

// TestAgainstEngines runs the development client library's engine-backed test
// suite against each supported engine fixture. The matrix initially contains
// only the engine built from the current source tree.
func TestAgainstEngines(t *testing.T) {
	t.Run("dev", func(t *testing.T) {
		h := newHarness(t)
		cliBin := h.devCLIBinary(t)
		engine, engineEndpoint := h.startDevEngine(t, "go-client-against-dev-engine")

		innerSource := h.dag.CurrentWorkspace().Directory("/sdk/go/e2e/testdata/against-engines")
		devSDKSource := h.dag.CurrentWorkspace().Directory("/sdk/go")

		target := h.dag.Container().
			From("golang:1.26-alpine").
			WithExec([]string{"apk", "add", "--no-cache", "ca-certificates", "coreutils", "git"}).
			WithServiceBinding("dagger-engine", engine).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/local/bin/dagger").
			WithoutEnvVariable("DAGGER_SESSION_PORT").
			WithoutEnvVariable("DAGGER_SESSION_TOKEN").
			WithFile("/usr/local/bin/dagger", cliBin, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithDirectory("/sdk", devSDKSource).
			WithDirectory("/work", innerSource).
			WithNewFile("/work/go.mod", `module dagger-against-engines-inner

go 1.26.1

require (
	dagger.io/dagger v0.0.0
	github.com/stretchr/testify v1.11.1
)

replace dagger.io/dagger => /sdk
`).
			WithFile("/work/go.sum", devSDKSource.File("go.sum")).
			WithWorkdir("/work")

		innerTest := target.WithExec(
			[]string{"timeout", "-k", "10s", "10m", "go", "test", "-v", "-count=1", "-mod=mod", "./..."},
			dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny, NoInit: true},
		)
		requireTargetExec(t, innerTest, "run the development client library against the dev engine")
	})
}
