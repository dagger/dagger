package main

import (
    "context"
    "dagger/my-module/internal/dagger"
    "dagger.io/dagger/dag"
)

// Demonstrates an SSH-based clone requiring a user-supplied sshAuthSocket.
//
// For the reasoning behind explicit socket forwarding, see:
// /path/to/security-by-design
// You can also avoid passing a socket if you prefer the Directory pattern,
// e.g. dagger call someFunc --dir git@github.com:org/repo@main
type MyModule struct{}

func (m *MyModule) CloneWithSsh(ctx context.Context, repository string, ref string, sock *dagger.Socket) *dagger.Container {
    d := dag.Git(repository, dagger.GitOpts{SSHAuthSocket: sock}).Ref(ref).Tree()

    return dag.Container().
        From("alpine:latest").
        WithDirectory("/src", d).
        WithWorkdir("/src")
}