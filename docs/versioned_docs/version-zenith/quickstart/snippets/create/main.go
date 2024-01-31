package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) Build(dir *Directory, args []string, p string) *File {

}

func (m *MyModule) BuildAndPublish(dir *Directory, args []string, p string, ctx context.Context) (string, error) {
	file := dag.
		Golang().
		WithProject(dir).
		Build(args).File(p)

	return dag.
		Wolfi().
		Base().
		Container().
		WithFile("/usr/local/bin/dagger", file).
		Publish(ctx, fmt.Sprintf("ttl.sh/myapp:10m"))

}
