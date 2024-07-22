package main

import (
	"context"

	"dagger.io/dagger"
)

type MyModule struct{}

// Build a Container from a Dockerfile
func (m *MyModule) Build(
	ctx context.Context,
	// The source code to build
	source *dagger.Directory,
	// The secret to use in the Dockerfile
	secret *dagger.Secret,
) (*dagger.Container, error) {
	secretName, err := secret.Name(ctx)
	if err != nil {
		return nil, err
	}

	return source.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "Dockerfile",
			BuildArgs: []dagger.BuildArg{
				{Name: "gh-secret", Value: secretName},
			},
			Secrets: []*dagger.Secret{secret},
		}), nil
}
