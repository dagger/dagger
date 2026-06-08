package main

import (
	"context"

	"dagger/my-module/internal/dagger"
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
	// Ensure the Dagger secret's name matches what the Dockerfile
	// expects as the id for the secret mount.
	secretVal, err := secret.Plaintext(ctx)
	if err != nil {
		return nil, err
	}
	buildSecret := dag.SetSecret("gh-secret", secretVal)

	return source.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{buildSecret},
		}), nil
}
