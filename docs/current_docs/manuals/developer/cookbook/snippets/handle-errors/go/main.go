package main

import (
	"context"
	"fmt"

	"dagger.io/dagger/dag"
)

type MyModule struct{}

// Generate an error
func (m *MyModule) Test(ctx context.Context) (string, error) {
	out, err := dag.
		Container().
		From("alpine").
		// ERROR: cat: read error: Is a directory
		WithExec([]string{"cat", "/"}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("Test pipeline failure: %w", err)
	}
	return out, nil
}
