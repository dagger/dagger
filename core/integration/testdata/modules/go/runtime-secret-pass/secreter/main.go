package main

import (
	"context"

	"dagger/secreter/internal/dagger"
)

type Secreter struct{}

func (_ *Secreter) Make() *dagger.Secret {
	return dag.SetSecret("FOO", "inner")
}

func (_ *Secreter) Get(ctx context.Context, secret *dagger.Secret) (string, error) {
	return secret.Plaintext(ctx)
}
