package main

import "dagger.io/dagger"

// Lint the Dagger engine code
func (e EngineTargets) Lint(ctx dagger.Context) (string, error) {
	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("engine").Pipeline("lint")

	out, err := c.Container().
		From("golangci/golangci-lint:v1.51").
		WithMountedDirectory("/app", e.SrcDir).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Stdout(ctx)
	return out, err
}
