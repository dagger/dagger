package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) ReadDir(
	ctx context.Context,
	// +defaultPath="/"
	source *dagger.Directory,
) ([]string, error) {
	return source.Entries(ctx)
}
