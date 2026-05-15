package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Fn(ctx context.Context, secret *dagger.Secret) (string, error) {
	return secret.Plaintext(ctx)
}

func (*Test) Fn2(ctx context.Context, secret *dagger.Secret) *dagger.Container {
	return dag.Container().From("alpine:3.20").
		WithSecretVariable("TOPSECRET", secret).
		WithExec([]string{"sh", "-c", "echo -n $(echo -n $TOPSECRET | base64)"})
}
