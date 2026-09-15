package main

import (
	"context"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (*Dep) GetEncoded(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("FOO", "shhh")
	return dag.Container().From("alpine:3.22.1").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
}

func (*Dep) GetCensored(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("BAR", "fdjsklajakldjfl")
	return dag.Container().From("alpine:3.22.1").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
}
