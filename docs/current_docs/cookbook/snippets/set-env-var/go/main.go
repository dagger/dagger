package main

import "context"

type MyModule struct{}

// Set a single environment variable in a container
func (m *MyModule) SetEnvVar(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine").
		WithEnvVariable("ENV_VAR", "VALUE").
		WithExec([]string{"env"}).
		Stdout(ctx)
}
