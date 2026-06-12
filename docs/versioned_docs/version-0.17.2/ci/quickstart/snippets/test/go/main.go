package main

import (
	"context"

	"dagger/hello-dagger/internal/dagger"
)

type HelloDagger struct{}

// Return the result of running unit tests
func (m *HelloDagger) Test(
	ctx context.Context,
	// +defaultPath="/"
	source *dagger.Directory,
) (string, error) {
	// get the build environment container
	// by calling another Dagger Function
	return m.BuildEnv(source).
		// call the test runner
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		// capture and return the command output
		Stdout(ctx)
}
