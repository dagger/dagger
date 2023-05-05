package main

import "dagger.io/dagger"

type Go struct {
	SrcDir *dagger.Directory
}

func (s SDK) Go(ctx dagger.Context) (Go, error) {
	return Go(s), nil
}

func (g Go) Lint(ctx dagger.Context) (string, error) {
	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("sdk").Pipeline("go").Pipeline("lint")

	out, err := c.Container().
		From("golangci/golangci-lint:v1.51").
		WithMountedDirectory("/app", g.SrcDir).
		WithWorkdir("/app/sdk/go").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	// TODO: test generated code matches

	return out, nil
}
