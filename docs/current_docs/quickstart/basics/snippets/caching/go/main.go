package main

import (
	"context"
	"dagger/basics/internal/dagger"
)

type Basics struct{}

func (m *Basics) Foo(ctx context.Context) (*dagger.Container, error) {
	aptCache := dag.CacheVolume("apt-cache")
	return dag.Container().
		From("debian:latest").
		WithMountedCache("/var/cache/apt/archives", aptCache).
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "--yes", "maven", "mariadb-server"}).
		Sync(ctx)
}
