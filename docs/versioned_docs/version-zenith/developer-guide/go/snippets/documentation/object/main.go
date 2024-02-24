package main

import (
	"context"
)

// The MyModule object is a simple example of documenting an object.
type MyModule struct{}

func (*MyModule) Version(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:3.14.0").
		WithExec([]string{"/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"}).
		Stdout(ctx)
}
