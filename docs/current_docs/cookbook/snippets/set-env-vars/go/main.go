package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

type EnvVar struct {
	Name  string
	Value string
}

// Set environment variables in a container
func (m *MyModule) SetEnv(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine").
		With(EnvVariables([]*EnvVar{
			{"ENV_VAR_1", "VALUE 1"},
			{"ENV_VAR_2", "VALUE 2"},
			{"ENV_VAR_3", "VALUE 3"},
		})).
		WithExec([]string{"env"}).
		Stdout(ctx)
}

func EnvVariables(envs []*EnvVar) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		for _, e := range envs {
			c = c.WithEnvVariable(e.Name, e.Value)
		}
		return c
	}
}
