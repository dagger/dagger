package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) GithubAuth(ctx context.Context, secret *Secret) (string, error) {
	return dag.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "github-cli"}).
		WithMountedSecret("/root/.config/gh/hosts.yml", secret).
		WithWorkdir("/root").
		WithExec([]string{"gh", "auth", "status"}).
		Stdout(ctx)
}
