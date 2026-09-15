package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

// Demonstrates cloning a Git repository over HTTP(S).
//
// For SSH usage, see the SSH snippet (CloneWithSsh).
type MyModule struct{}

func (m *MyModule) Clone(ctx context.Context, repository string, ref string) *dagger.Container {
	d := dag.Git(repository).Ref(ref).Tree()

	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", d).
		WithWorkdir("/src")
}
