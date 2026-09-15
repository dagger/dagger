package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, svc *dagger.Service) (string, error) {
	return dag.Container().From("alpine:3.22.1").WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("daserver", svc).
		WithExec([]string{"curl", "http://daserver"}).
		Stdout(ctx)
}
