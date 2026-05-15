package main

import "context"

type Test struct{}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	secret := dag.SetSecret("FOO", "shhh")
	ctr := dag.Container().From("alpine:3.22.1").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
	return dag.Dep().Get(ctx, ctr)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	secret := dag.SetSecret("BAR", "fdlaskfjdlsajfdkasl")
	ctr := dag.Container().From("alpine:3.22.1").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
	return dag.Dep().Get(ctx, ctr)
}
