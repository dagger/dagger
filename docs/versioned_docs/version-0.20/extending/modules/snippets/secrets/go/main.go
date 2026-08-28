package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) ShowSecret(
	ctx context.Context,
	token *dagger.Secret,
) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithSecretVariable("MY_SECRET", token).
		WithExec([]string{"sh", "-c", `echo this is the secret: $MY_SECRET`}).
		Stdout(ctx)
}
