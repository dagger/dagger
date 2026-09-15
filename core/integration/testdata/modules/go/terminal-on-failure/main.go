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
			WithMountedDirectory("/a_mnt", d1).
			WithMountedCache("/cachemnt", dag.CacheVolume("somethingoranother")).
			WithMountedDirectory("/z_mnt", d2).
			WithExec([]string{"sh", "-c",
				"echo breakpoint > /fail && echo FOOFOO > /a_mnt/foo && echo BARBAR > /z_mnt/bar && exit 42",
			}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
