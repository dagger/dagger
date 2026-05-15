package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Fn(ctx context.Context, secret *dagger.Secret) (*dagger.Container, error) {
	return dag.Container().From("alpine:3.20").
		WithSecretVariable("TOPSECRET", secret).
		Sync(ctx)
}
