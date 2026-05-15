package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func New() *Test {
	t := &Test{}
	secret := dag.SetSecret("FOO", "omfg")
	t.Ctr = dag.Container().From("alpine:3.22.1").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
