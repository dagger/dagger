package main

import (
	"context"
)

type Test struct{}

func (*Test) Fn(ctx context.Context, rand string) (string, error) {
	s := dag.SetSecret("FOO", "bar")
	return dag.Container().From("alpine:3.20").
		WithSecretVariable("FOO", s).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
}
