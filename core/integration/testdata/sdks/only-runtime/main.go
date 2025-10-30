package main

import (
	"context"
	"dagger/only-runtime/internal/dagger"
)

type OnlyRuntime struct {
	Src *dagger.Directory
}

func New(
	//+defaultPath="./src"
	sdkSrc *dagger.Directory,
) *OnlyRuntime {
	return &OnlyRuntime{
		Src: sdkSrc,
	}
}

func (m *OnlyRuntime) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	return dag.Container().
		From("golang:1.25.3-alpine").
		WithDirectory("/src", m.Src).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/bin/sdk", "."}).
		WithEntrypoint([]string{"/bin/sdk"}), nil
}
