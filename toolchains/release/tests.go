package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"toolchains/release/internal/dagger"

	"golang.org/x/mod/semver"
)

// Create a fake release a run checks to catch potential breaking changes.
func (r *Release) TestLocalRelease(
	ctx context.Context,
	// Current engine version. The test runs the next patch (vX.Y.Z+1) on top.
	version string,
) (*ReleaseTest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &ReleaseTest{
		Container: dag.EngineDev(dagger.EngineDevOpts{Ws: r.Workspace}).Playground(
			dagger.EngineDevPlaygroundOpts{Version: bumpVersionByPatch(version)},
		),
	}, nil
}

type ReleaseTest struct {
	Container *dagger.Container
}

// Test scaffolding a new module via the Go SDK and executing basic commands.
//
// This installs the Go SDK, uses `dagger module init` with the legacy template
// (the classic ContainerEcho/GrepDir example) to scaffold a module, then calls
// the generated module. Workspace discovery resolves through a .git boundary,
// so the working directory is initialized as a repo first.
// +check
func (r *ReleaseTest) NewModule(ctx context.Context) error {
	ctr := r.Container.WithWorkdir("/work")

	ctr, err := ctr.WithExec([]string{"git", "init"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize workspace git repo: %w", err)
	}

	ctr, err = ctr.WithExec([]string{"dagger", "sdk", "install", "go"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to install go-sdk into workspace: %w", err)
	}

	ctr, err = ctr.WithExec([]string{"dagger", "-y", "module", "init", "go", "my-module", "--template=legacy"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to scaffold a new module: %w", err)
	}

	_, err = ctr.WithExec([]string{"dagger", "-m", ".dagger/modules/my-module", "call", "container-echo", "--string-arg", "hello-world", "sync"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to run dagger call on scaffolded module: %w", err)
	}

	return nil
}

// Test calling an existing module with basic commands.
// +check
func (r *ReleaseTest) ExistingModule(
	ctx context.Context,

	//+defaultPath="/toolchains/release/testdata/module"
	testdata *dagger.Directory,
) error {
	ctr := r.Container.
		WithDirectory("/work/module", testdata).
		WithWorkdir("/work/module")

	_, err := ctr.WithExec([]string{"dagger", "call", "-m", ".", "container-echo", "--string-arg", "hello-world", "sync"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to run dagger call on existing module: %w", err)
	}

	return nil
}

// Bump the given version by a patch.
// For example: v0.19.4 will become v0.19.5
func bumpVersionByPatch(version string) string {
	// Ensure the version is canonical (e.g. "v1.2.3")
	version = semver.Canonical(version)
	if version == "" {
		return ""
	}

	// Strip the leading "v" and split into parts
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) < 3 {
		return ""
	}

	// Parse and increment the patch version
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return ""
	}

	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1)
}
