package main

import (
	"context"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) ListFiles(ctx context.Context, dir *dagger.Directory) ([]string, error) {
	return dir.Entries(ctx)
}
