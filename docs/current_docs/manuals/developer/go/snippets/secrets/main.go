package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) GithubApi(ctx context.Context, endpoint string, token *Secret) (string, error) {
	return dag.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "curl"}).
		WithSecretVariable("GITHUB_TOKEN", token).
		WithExec([]string{"sh", "-c", fmt.Sprintf("curl \"%s\" --header \"Accept: application/vnd.github+json\" --header \"Authorization: Bearer $GITHUB_TOKEN\"", endpoint)}).
		Stdout(ctx)
}
