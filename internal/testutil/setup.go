package testutil

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/dagger/internal/testutil/dagger/dag"
)

// FinalizeTestMain performs the common final setup steps for test suites that
// need a running inner engine. It exports the CLI binary, sets the runner host
// to the given engine service, and unsets session vars so that tests (and CLI
// subprocesses) create fresh sessions against the inner engine.
//
// Callers must complete all dag.* calls that depend on the outer session
// BEFORE calling this function, since it unsets DAGGER_SESSION_PORT and
// DAGGER_SESSION_TOKEN.
func FinalizeTestMain(ctx context.Context, engineSvc *dagger.Service) {
	// Export CLI binary
	_, err := dag.Cli().Binary().Export(ctx, "/.dagger-cli")
	if err != nil {
		panic(fmt.Sprintf("failed to export CLI binary: %v", err))
	}
	os.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", "/.dagger-cli")
	os.Setenv("_TEST_DAGGER_CLI_LINUX_BIN", "/.dagger-cli")

	// Set inner engine as runner host
	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		panic(fmt.Sprintf("failed to get engine endpoint: %v", err))
	}
	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint)

	// Unset session vars so tests create fresh sessions against inner engine
	// (must happen after all dag.* calls that need the outer session)
	os.Unsetenv("DAGGER_SESSION_PORT")
	os.Unsetenv("DAGGER_SESSION_TOKEN")
}
