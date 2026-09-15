package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	d1 := dag.Directory().WithNewFile("foo", "FOO\n")
	d2 := dag.Directory().WithNewFile("bar", "BAR\n")

	return &Test{
		Ctr: dag.Container().
			From("alpine:3.22.1").
			WithEnvVariable("COOLENV", "woo").
			WithWorkdir("/coolworkdir").
			WithMountedDirectory("/a_mnt", d1).
			WithMountedCache("/cachemnt", dag.CacheVolume("whateverbrah")).
			WithMountedDirectory("/z_mnt", d2).
			WithDefaultTerminalCmd([]string{"/bin/sh"}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
