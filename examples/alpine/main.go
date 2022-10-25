package main

import (
	"context"

	"dagger.io/dagger"
)

func main() {
	dagger.Serve(
		Alpine{},
	)
}

type Alpine struct {
}

func (a Alpine) Build(ctx context.Context, pkgs []string) (*dagger.Container, error) {
	client, err := dagger.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// start with Alpine base
	alpine := client.Container().From("alpine:3.15")

	// install each of the requested packages
	for _, pkg := range pkgs {
		alpine = alpine.Exec(dagger.ContainerExecOpts{
			Args: []string{"apk", "add", "-U", "--no-cache", pkg},
		})
	}

	return alpine, nil
}
