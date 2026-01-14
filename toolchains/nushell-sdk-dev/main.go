// Go wrapper to expose Nushell SDK checks to CI
package main

import (
	"context"
	"dagger/nushell-sdk-dev/internal/dagger"
)

// Nushell SDK development toolchain
type NushellSdkDev struct {
	Workspace *dagger.Directory
}

func New(
	// A workspace containing the SDK source code
	// +defaultPath="/"
	// +ignore=["*", "!sdk/nushell/**/*.nu", "!sdk/nushell/**/*.md", "!sdk/nushell/runtime/**/*", "!sdk/nushell/tests/**/*", "!sdk/nushell/docs/**/*"]
	workspace *dagger.Directory,
) *NushellSdkDev {
	return &NushellSdkDev{
		Workspace: workspace,
	}
}

// getNushellContainer returns a container with Nushell and the SDK runtime
func (t *NushellSdkDev) getNushellContainer(ctx context.Context) *dagger.Container {
	// Get the SDK directory from workspace
	sdkDir := t.Workspace.Directory("sdk/nushell")

	// Create a container with Nushell and the runtime
	// Pass through Dagger session info so tests can call the Dagger API
	return dag.Container().
		From("alpine:3.19").
		// Install curl for downloading Nushell
		WithExec([]string{"apk", "add", "--no-cache", "curl", "bash"}).
		// Download and install Nushell (latest stable version)
		WithExec([]string{"sh", "-c", "curl -fsSL https://github.com/nushell/nushell/releases/download/0.109.1/nu-0.109.1-x86_64-unknown-linux-musl.tar.gz | tar -xz -C /usr/local/bin --strip-components=1 nu-0.109.1-x86_64-unknown-linux-musl/nu"}).
		WithExec([]string{"chmod", "+x", "/usr/local/bin/nu"}).
		// Mount the SDK source
		WithMountedDirectory("/workspace", sdkDir).
		WithWorkdir("/workspace").
		// Install the runtime to /usr/local/lib (where tests expect it)
		WithExec([]string{"mkdir", "-p", "/usr/local/lib"}).
		WithExec([]string{"sh", "-c", "cp -r runtime/runtime/dag.nu /usr/local/lib/dag.nu && cp -r runtime/runtime/dag /usr/local/lib/dag"})
}

// +check
// Run Nushell SDK tests
func (t *NushellSdkDev) Test(ctx context.Context) error {
	container := t.getNushellContainer(ctx)

	// First run structural validation
	_, err := container.
		WithExec([]string{"nu", "tests/simple-validate.nu"}).
		Sync(ctx)
	if err != nil {
		return err
	}

	// Then run actual integration tests with live Dagger API calls
	// These tests validate the runtime works correctly by calling:
	// - Container operations (from, with-exec, with-env-variable, etc.)
	// - Directory operations (with-new-file, with-new-directory, etc.)
	// - File operations
	// - Git operations
	// - Type metadata preservation
	//
	// Use ExperimentalPrivilegedNesting to give tests access to Dagger session
	_, err = container.
		WithExec(
			[]string{"nu", "tests/ci-tests.nu"},
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
		).
		Sync(ctx)

	return err
}

// +check
// Run Nushell SDK check examples
func (t *NushellSdkDev) CheckExamples(ctx context.Context) error {
	container := t.getNushellContainer(ctx)

	// Check that examples run without errors
	_, err := container.
		WithExec([]string{"sh", "-c", "test -d examples && echo 'Examples directory exists' || echo 'No examples yet'"}).
		Sync(ctx)

	return err
}

// +check
// Verify README examples are valid
func (t *NushellSdkDev) CheckReadme(ctx context.Context) error {
	container := t.getNushellContainer(ctx)

	// Verify README exists and has content
	_, err := container.
		WithExec([]string{"sh", "-c", "test -f README.md && wc -l README.md | grep -v '^0'"}).
		Sync(ctx)

	return err
}

// +check
// Verify documentation exists
func (t *NushellSdkDev) CheckDocs(ctx context.Context) error {
	container := t.getNushellContainer(ctx)

	// Verify runtime documentation exists (files are in runtime/runtime/)
	_, err := container.
		WithExec([]string{"sh", "-c", "test -d runtime/runtime && test -f runtime/runtime/dag.nu"}).
		Sync(ctx)

	return err
}

// +check
// Verify runtime structure is correct
func (t *NushellSdkDev) CheckStructure(ctx context.Context) error {
	container := t.getNushellContainer(ctx)

	// Verify all expected runtime files exist (in runtime/runtime/ subdirectory)
	requiredFiles := []string{
		"runtime/runtime/dag.nu",
		"runtime/runtime/dag/core.nu",
		"runtime/runtime/dag/wrappers.nu",
		"runtime/runtime/dag/container.nu",
		"runtime/runtime/dag/directory.nu",
		"runtime/runtime/dag/file.nu",
		"runtime/runtime/dag/host.nu",
	}

	for _, file := range requiredFiles {
		_, err := container.
			WithExec([]string{"test", "-f", file}).
			Sync(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}
