package main

import (
	"context"
)

type MyModule struct{}

// Set environment variables in a container
func (m *MyModule) SetEnv(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine").
		With(EnvVariables(map[string]string{
			"ENV_VAR_1": "VALUE 1",
			"ENV_VAR_2": "VALUE 2",
			"ENV_VAR_3": "VALUE 3",
		})).
		WithExec([]string{"env"}).
		Stdout(ctx)

}

func EnvVariables(envs map[string]string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		for key, value := range envs {
			c = c.WithEnvVariable(key, value)
		}
		return c
	}
}
