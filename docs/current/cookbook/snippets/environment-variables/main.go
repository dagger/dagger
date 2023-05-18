package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	// create Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// setup container and
	// define environment variables
	ctr := client.
		Container().
		From("alpine").
		With(EnvVariables(map[string]string{
			"ENV_VAR_1": "VALUE 1",
			"ENV_VAR_2": "VALUE 2",
			"ENV_VAR_3": "VALUE 3",
		})).
		WithExec([]string{"env"})

	// print environment variables
	out, err := ctr.Stdout(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
}

func EnvVariables(envs map[string]string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		for key, value := range envs {
			c = c.WithEnvVariable(key, value)
		}
		return c
	}
}
