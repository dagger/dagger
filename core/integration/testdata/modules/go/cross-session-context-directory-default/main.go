package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Entries(
	ctx context.Context,
	cacheBust string,
	// +defaultPath="/"
	dir *dagger.Directory,
) ([]string, error) {
	return dir.Entries(ctx)
}
