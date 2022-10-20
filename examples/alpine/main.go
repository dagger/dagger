package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/api"
)

func main() {
	dagger.Serve(
		Alpine{},
	)
}

type Alpine struct {
}

func (a Alpine) Build(ctx dagger.Context, pkgs []string) (*api.Container, error) {
	client, err := dagger.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// start with Alpine base
	alpine := client.Core().Container().From("alpine:3.15")

	// install each of the requested packages
	for _, pkg := range pkgs {
		alpine = alpine.Exec(api.ContainerExecOpts{
			Args: []string{"apk", "add", "-U", "--no-cache", pkg},
		})
	}

	return alpine, nil
}
