package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) GithubAuth(ctx context.Context, secret *Secret) (string, error) {
	return dag.Container().
		From("alpine:3.17").
		WithSecretVariable("GITHUB_API_TOKEN", secret).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"sh", "-c", `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`}).
		Stdout(ctx)
}
