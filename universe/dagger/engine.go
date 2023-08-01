package main

import "dagger.io/dagger"

// Lint the Dagger engine code
func EngineLint(ctx dagger.Context) (string, error) {
	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("engine").Pipeline("lint")

	out, err := c.Container().
		From("golangci/golangci-lint:v1.51-alpine").
		WithMountedDirectory("/app", srcDir(ctx)).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Stdout(ctx)
	return out, err
}

// Build the Dagger CLI
func Cli(ctx dagger.Context) (*dagger.Directory, error) {
	return ctx.Client().Directory().WithFile("bin/dagger", cli(ctx)), nil
}

func cli(ctx dagger.Context) *dagger.File {
	return ctx.Client().Container().
		From("golang:1.20-alpine").
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", ctx.Client().CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", ctx.Client().CacheVolume("go-build")).
		WithMountedDirectory("/app", srcDir(ctx)).
		WithWorkdir("/app").
		WithExec([]string{"go", "build", "-o", "./bin/dagger", "./cmd/dagger"}).
		File("./bin/dagger")
}

func DevShell(ctx dagger.Context) (*dagger.Container, error) {
	return ctx.Client().Container().From("alpine:3.18").
		WithMountedFile("/usr/local/bin/dagger", cli(ctx)).
		WithNewFile("/cat-this-file", dagger.ContainerWithNewFileOpts{
			Contents: "you're shellin like a felon 😎\n",
		}), nil
}
