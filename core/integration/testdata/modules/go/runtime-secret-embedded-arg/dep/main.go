package main

import (
	"context"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (*Dep) Get(ctx context.Context, ctr *dagger.Container) (string, error) {
	return ctr.Stdout(ctx)
}
