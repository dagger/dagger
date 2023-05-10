package main

import "dagger.io/dagger"

type GoTargets struct {
	SrcDir *dagger.Directory
}

func (s SDKTargets) Go(ctx dagger.Context) (GoTargets, error) {
	return GoTargets(s), nil
}

// Lint the Dagger Go SDK
func (g GoTargets) Lint(ctx dagger.Context) (string, error) {
	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("sdk").Pipeline("go").Pipeline("lint")

	out, err := c.Container().
		From("golangci/golangci-lint:v1.51").
		WithMountedDirectory("/app", g.SrcDir).
		WithWorkdir("/app/sdk/go").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Stderr(ctx)
	if err != nil {
		return "", err
	}

	// TODO: test generated code matches

	return out, nil
}
