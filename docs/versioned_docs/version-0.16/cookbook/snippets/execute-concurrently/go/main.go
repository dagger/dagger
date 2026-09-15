package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"

	"golang.org/x/sync/errgroup"
)

// Constructor
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
func (m *MyModule) RunAllTests(ctx context.Context) error {
	// Create error group
	eg, gctx := errgroup.WithContext(ctx)

	// Run linter
	eg.Go(func() error {
		_, err := m.Lint(gctx)
		return err
	})

	// Run type-checker
	eg.Go(func() error {
		_, err := m.Typecheck(gctx)
		return err
	})

	// Run unit tests
	eg.Go(func() error {
		_, err := m.Test(gctx)
		return err
	})

	// Wait for all tests to complete
	// If any test fails, the error will be returned
	return eg.Wait()
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
