package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context) *dagger.Container {
	redis := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	cli := dag.Container().
		From("redis").
		WithoutEntrypoint().
		WithServiceBinding("redis", redis)

	ctrA := cli.WithExec([]string{"sh", "-c", "redis-cli -h redis info >> /tmp/out.txt"})

	file := ctrA.Directory("/tmp").File("/out.txt")

	ctrB := dag.Container().
		From("alpine:3.22.1").
		WithFile("/out.txt", file)

	return ctrB.WithExec([]string{"cat", "/out.txt"})
}
