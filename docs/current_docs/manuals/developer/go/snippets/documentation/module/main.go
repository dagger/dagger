// This module returns information about the OS for a container image
package main

// Further documentation for the module here.

import (
	"context"
)

type MyModule struct{}

func (*MyModule) Version(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:3.14.0").
		WithExec([]string{"/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"}).
		Stdout(ctx)
}
