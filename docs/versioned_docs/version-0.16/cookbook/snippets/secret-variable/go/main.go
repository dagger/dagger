package main

import (
	"context"

	"main/internal/dagger"
)

type MyModule struct{}

// Query the GitHub API
func (m *MyModule) GithubApi(
	ctx context.Context,
	// GitHub API token
	token *dagger.Secret,
) (string, error) {
	return dag.Container().
		From("alpine:3.17").
		WithSecretVariable("GITHUB_API_TOKEN", token).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"sh", "-c", `curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN"`}).
		Stdout(ctx)
}
