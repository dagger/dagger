package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

// Demonstrates an SSH-based clone requiring a user-supplied SSHAuthSocket.
type MyModule struct{}

func (m *MyModule) CloneWithSsh(ctx context.Context, repository string, ref string, sock *dagger.Socket) *dagger.Container {
	d := dag.Git(repository, dagger.GitOpts{SSHAuthSocket: sock}).Ref(ref).Tree()

	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", d).
		WithWorkdir("/src")
}
