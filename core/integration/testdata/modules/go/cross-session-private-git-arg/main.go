package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, dir *dagger.Directory, rand string) ([]string, error) {
	return dir.Entries(ctx)
}
