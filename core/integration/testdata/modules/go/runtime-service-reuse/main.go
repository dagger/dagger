package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) FnA(ctx context.Context) (*Sub, error) {
	svc := dag.Container().
		From("python").
		WithMountedDirectory(
			"/srv/www",
			dag.Directory().WithNewFile("index.html", "hey there"),
		).
		WithWorkdir("/srv/www").
		WithExposedPort(23457).
		WithDefaultArgs([]string{"python", "-m", "http.server", "23457"}).
		AsService()

	ctr := dag.Container().
		From("alpine:3.22.1").
		WithServiceBinding("svc", svc).
		WithExec([]string{"wget", "-O", "-", "http://svc:23457"})

	out, err := ctr.Stdout(ctx)
	if err != nil {
		return nil, err
	}
	if out != "hey there" {
		return nil, fmt.Errorf("unexpected output: %q", out)
	}
	return &Sub{Ctr: ctr}, nil
}

type Sub struct {
	Ctr *dagger.Container
}

func (m *Sub) FnB(ctx context.Context) (string, error) {
	return m.Ctr.
		WithExec([]string{"wget", "-O", "-", "http://svc:23457"}).
		Stdout(ctx)
}
