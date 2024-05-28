package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, source *Directory, secret *Secret) (*Container, error) {
	secretName, err := secret.Name(ctx)
	if err != nil {
		return nil, err
	}

	return source.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "Dockerfile",
			BuildArgs: []BuildArg{
					{Name: "gh-secret", Value: secretName},
				},
			Secrets:    []*dagger.Secret{secret},
		})
}
