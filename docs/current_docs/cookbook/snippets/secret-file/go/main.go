package main

import (
	"context"

	"main/internal/dagger"
)

type MyModule struct{}

// Query the GitHub API
func (m *MyModule) GithubAuth(
	ctx context.Context,
	// GitHub Hosts configuration file
	ghCreds *dagger.Secret,
) (string, error) {
	return dag.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "github-cli"}).
		WithMountedSecret("/root/.config/gh/hosts.yml", ghCreds).
		WithWorkdir("/root").
		WithExec([]string{"gh", "auth", "status"}).
		Stdout(ctx)
}
