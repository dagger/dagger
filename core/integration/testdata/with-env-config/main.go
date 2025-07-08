package main

import (
	"context"
	"dagger/acme/internal/dagger"
)

func New(
	ghToken *dagger.Secret,
) Acme {
	return Acme{
		GhToken: ghToken,
	}
}

type Acme struct {
	GhToken *dagger.Secret
}

func (m Acme) Plaintext(ctx context.Context) (string, error) {
	return m.GhToken.Plaintext(ctx)
}
