package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) GithubApi(ctx context.Context, file *Secret) (string, error) {
	return dag.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "github-cli"}).
		WithMountedSecret("/root/.config/gh/hosts.yml", file).
		WithWorkdir("/root").
		WithExec([]string{"gh", "auth", "status"}).
		Stdout(ctx)
}
