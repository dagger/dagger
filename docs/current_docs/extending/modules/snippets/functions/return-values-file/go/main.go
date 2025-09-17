package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Archiver(ctx context.Context, src *dagger.Directory) *dagger.File {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "zip"}).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"sh", "-c", "zip -p -r out.zip *.*"}).
		File("/src/out.zip")
}
