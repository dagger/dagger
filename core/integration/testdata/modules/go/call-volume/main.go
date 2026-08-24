package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Read(ctx context.Context, volume *dagger.Volume) (string, error) {
	return dag.Container().
		From("alpine:3.22.1").
		WithMountedVolume("/mnt", volume, dagger.ContainerWithMountedVolumeOpts{ReadOnly: true}).
		WithExec([]string{"cat", "/mnt/hello.txt"}).
		Stdout(ctx)
}
