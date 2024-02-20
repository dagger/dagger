package main

import (
	"context"
	"fmt"

	"dagger.io/dagger/dag"
)

type MyModule struct{}

func (m *MyModule) GithubApi(ctx context.Context, endpoint string, token *Secret) (string, error) {
	plaintext, err := token.Plaintext(ctx)
	if err != nil {
		return "", err
	}
	return dag.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"sh", "-c", fmt.Sprintf("curl \"%s\" --header \"Accept: application/vnd.github+json\" --header \"Authorization: Bearer %s\"", endpoint, plaintext)}).
		Stdout(ctx)
}
