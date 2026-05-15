package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Insecure(ctx context.Context, token *dagger.Secret) (string, error) {
	return token.Plaintext(ctx)
}
