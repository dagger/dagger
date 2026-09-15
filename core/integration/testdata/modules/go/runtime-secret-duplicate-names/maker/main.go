package main

import (
	"context"

	"dagger/maker/internal/dagger"
)

type Maker struct{}

func (_ *Maker) MakeSecret(ctx context.Context) (*dagger.Secret, error) {
	secret := dag.SetSecret("FOO", "inner")
	_, err := secret.ID(ctx)
	if err != nil {
		return nil, err
	}
	return secret, nil
}
