package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"

	"golang.org/x/sync/errgroup"
)

type MyModule struct{}

// Return the result of running unit tests
func (m *MyModule) Test(ctx context.Context, source *dagger.Directory) (string, error) {
	return m.BuildEnv(source).
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}

// Return the result of running the linter
func (m *MyModule) Lint(ctx context.Context, source *dagger.Directory) (string, error) {
	return m.BuildEnv(source).
		WithExec([]string{"npm", "run", "lint"}).
		Stdout(ctx)
}

// Return the result of running the type-checker
func (m *MyModule) Typecheck(ctx context.Context, source *dagger.Directory) (string, error) {
	return m.BuildEnv(source).
		WithExec([]string{"npm", "run", "type-check"}).
		Stdout(ctx)
}

// Run linter, type-checker, unit tests concurrently
func (m *MyModule) RunAllTests(ctx context.Context, source *dagger.Directory) (string, error) {
	var testResult, lintResult, typecheckResult string
	var testErr, lintErr, typecheckErr error

	// Create error group
	eg, gctx := errgroup.WithContext(ctx)

	// Run linter
	eg.Go(func() error {
		lintResult, lintErr = m.Lint(gctx, source)
		return lintErr
	})

	// Run type-checker
	eg.Go(func() error {
		typecheckResult, typecheckErr = m.Typecheck(gctx, source)
		return typecheckErr
	})

	// Run unit tests
	eg.Go(func() error {
		testResult, testErr = m.Test(gctx, source)
		return testErr
	})

	// Wait for all tests to complete
	// If any test fails, return the error
	if err := eg.Wait(); err != nil {
		return "", fmt.Errorf("error: %w", err)
	}

	// If all tests succeed, print the test results
	return testResult + lintResult + typecheckResult, nil
}

// Build a ready-to-use development environment
func (m *MyModule) BuildEnv(source *dagger.Directory) *dagger.Container {
	nodeCache := dag.CacheVolume("node")
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source).
		WithMountedCache("/root/.npm", nodeCache).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"})
}
