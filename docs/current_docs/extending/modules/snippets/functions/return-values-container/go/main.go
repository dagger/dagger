package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) AlpineBuilder(ctx context.Context, packages []string) *dagger.Container {
	ctr := dag.Container().
		From("alpine:latest")
	for _, pkg := range packages {
		ctr = ctr.WithExec([]string{"apk", "add", pkg})
	}
	return ctr
}
