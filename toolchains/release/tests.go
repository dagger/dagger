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
func (r *Release) TestLocalRelease(ctx context.Context) (*ReleaseTest, error) {
	v, err := dag.Version().Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current engine version: %w", err)
	}

	return &ReleaseTest{
		Container: dag.EngineDev().Playground(
			dagger.EngineDevPlaygroundOpts{Version: bumpVersionByPatch(v)},
		),
	}, nil
}

type ReleaseTest struct {
	Container *dagger.Container
}

// Test creating a new module and executing basic commands
// +check
func (r *ReleaseTest) NewModule(ctx context.Context) error {
	ctr := r.Container.WithWorkdir("/work/module")

	ctr, err := ctr.WithExec([]string{"dagger", "module", "init", "--name=my-module", "--sdk=go", "--source=."}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize a new module: %w", err)
	}

	_, err = ctr.WithExec([]string{"dagger", "call", "container-echo", "--string-arg", "hello-world", "sync"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to run dagger call on existing module: %w", err)
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

	_, err := ctr.WithExec([]string{"dagger", "develop"}).Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to run dagger develop on existing module: %w", err)
	}

	_, err = ctr.WithExec([]string{"dagger", "call", "container-echo", "--string-arg", "hello-world", "sync"}).Sync(ctx)
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
