package main

import "dagger.io/dagger"

// Lint the Dagger engine code
func (e EngineTargets) Lint(ctx dagger.Context) (string, error) {
	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("engine").Pipeline("lint")

	out, err := c.Container().
		From("golangci/golangci-lint:v1.51-alpine").
		WithMountedDirectory("/app", e.SrcDir).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}, dagger.ContainerWithExecOpts{
			Focus: true,
		}).
		Stdout(ctx)
	return out, err
}

func (e EngineTargets) Cli(ctx dagger.Context) (*dagger.Directory, error) {
	bin := ctx.Client().Container().
		From("golang:1.20-alpine").
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", ctx.Client().CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", ctx.Client().CacheVolume("go-build")).
		WithMountedDirectory("/app", e.SrcDir).
		WithWorkdir("/app").
		WithExec([]string{"go", "build", "-o", "./bin/dagger", "./cmd/dagger"}).
		File("./bin/dagger")
	return ctx.Client().Directory().WithFile("bin/dagger", bin), nil
}
