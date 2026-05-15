package main

import (
	"context"

	"dagger/leaker/internal/dagger"
)

type Leaker struct{}

func (l *Leaker) Leak(ctx context.Context, target string) string {
	secret, _ := dag.LoadSecretFromID(dagger.SecretID(target)).Plaintext(ctx)
	return secret
}
