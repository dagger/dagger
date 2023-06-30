package main

import (
	"context"

	"dagger.io/dagger"
)

func main() {
	env := dagger.NewEnvironment().
		WithTool(
			"progrocker",
			"Dead simple CLI tool to develop progrock",
			Generate, Test,
		).
		WithTool(
			"biome",
			"Utility for managing Progrock's Nix development container (for advanced users only)",
			Biome, NixBase, Nixpkgs,
		).
		WithShell("biome", Biome).
		// Add env-specific data to the graph,
		// for scripting, inspection and composition
		WithGraph(
			Biome,
			Generate,
			Nixpkgs,
			NixBase,
			NixDerivation,
		).
		WithCheck("go", "Go test suite",
			func(ctx dagger.Context) (dagger.Check, error) {
				return new(goTestSuite), nil
			},
		).
		WithExtension("nix", dagger.Git("github.com/vito/nix-env:v1")).
		WithWarmup(
			func(ctx dagger.Context, area string) error {
				Biome.Sync(ctx)
			},
		)
	env.Serve(context.Background())
}

type goTestSuite struct {
	exitCode int
	stdout   string
}

func (t *goTestSuite) run(ctx dagger.Context) error {
	ctr, err := Biome(ctx).
		Focus().
		WithExec([]string{
			"gotestsum",
			"--format=testname",
			"--no-color=false",
			"--json-file", "result.json",
			"./...",
		})
	stdout, err := ctr.Stdout(ctx)
	if err != nil {
		return nil, err
	}
	exitCode, err := ctr.ExitCode(ctx)
	if err != nil {
		return nil, err
	}
	t.stdout = stdout
	t.exitCode = exitCode
	return nil
}

func (t *GoTests) Result(ctx dagger.Context) (*dagger.TestResult, error) {
	if err := t.run(ctx); err != nil {
		return nil, err
	}
	return (t.exitCode == 0), nil
}

func (t *GoTest) SubChecks() (dagger.Check, error) {
	// FIXME: parse report file to break out each go test as a subcheck
	return nil, nil
}

func Generate(ctx dagger.Context) (*dagger.Directory, error) {
	return Biome(ctx).
		Focus().
		WithExec([]string{"go", "generate", "./..."}).
		Directory("/src"), nil
}

func Biome(ctx dagger.Context) *dagger.Container {
	return Nixpkgs(ctx, Flake(ctx),
		"bashInteractive",
		"go_1_20",
		"protobuf",
		"protoc-gen-go",
		"protoc-gen-go-grpc",
		"gotestsum",
	).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithMountedCache("/go/pkg/mod", ctx.Client().CacheVolume("go-mod")).
		WithMountedCache("/go/build-cache", ctx.Client().CacheVolume("go-build")).
		WithMountedDirectory("/src", Code(ctx)).
		WithWorkdir("/src")
}

func Code(ctx dagger.Context) *dagger.Directory {
	return ctx.Client().Host().Directory(".", dagger.HostDirectoryOpts{
		Include: []string{
			"**/*.go",
			"**/go.mod",
			"**/go.sum",
			"**/testdata/**/*",
			"**/*.proto",
			"**/*.tmpl",
		},
	})
}

func Flake(ctx dagger.Context) *dagger.Directory {
	return ctx.Client().Host().Directory(".", dagger.HostDirectoryOpts{
		// NB: maintain this as-needed, in case the Nix code sprawls
		Include: []string{"flake.nix", "flake.lock"},
	})
}
