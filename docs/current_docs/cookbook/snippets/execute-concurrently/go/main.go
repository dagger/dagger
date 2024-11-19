package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/my-module/internal/dagger"

	"golang.org/x/sync/errgroup"
)

func New(
	source *dagger.Directory,
) *MyModule {
	return &MyModule{
		Source: source,
	}
}

type MyModule struct {
	Source *dagger.Directory
}

// Return the result of running unit tests
func (m *MyModule) Test(ctx context.Context) (string, error) {
	return m.BuildEnv().
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}

// Return the result of running the linter
func (m *MyModule) Lint(ctx context.Context) (string, error) {
	return m.BuildEnv().
		WithExec([]string{"npm", "run", "lint"}).
		Stdout(ctx)
}

// Return the result of running the type-checker
func (m *MyModule) Typecheck(ctx context.Context) (string, error) {
	return m.BuildEnv().
		WithExec([]string{"npm", "run", "type-check"}).
		Stdout(ctx)
}

// Run linter, type-checker, unit tests concurrently
func (m *MyModule) RunAllTests(ctx context.Context) (string, error) {
	var testResult, lintResult, typecheckResult string
	var testErr, lintErr, typecheckErr error

	// Create error group
	eg, gctx := errgroup.WithContext(ctx)

	// Run linter
	eg.Go(func() error {
		lintResult, lintErr = m.Lint(gctx)
		return lintErr
	})

	// Run type-checker
	eg.Go(func() error {
		typecheckResult, typecheckErr = m.Typecheck(gctx)
		return typecheckErr
	})

	// Run unit tests
	eg.Go(func() error {
		testResult, testErr = m.Test(gctx)
		return testErr
	})

	// Wait for all tests to complete
	// If any test fails, return the error
	if err := eg.Wait(); err != nil {
		return "", fmt.Errorf("error: %w", err)
	}

	// If all tests succeed, print the test results
	return strings.Join([]string{testResult, lintResult, typecheckResult}, "\n"), nil
}

// Build a ready-to-use development environment
func (m *MyModule) BuildEnv() *dagger.Container {
	nodeCache := dag.CacheVolume("node")
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", m.Source).
		WithMountedCache("/root/.npm", nodeCache).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"})
}
