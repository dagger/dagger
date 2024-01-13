package mage

import (
	"context"
	"os"

	"github.com/dagger/dagger/internal/mage/util"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"
)

type Docs mg.Namespace

const (
	generatedSchemaPath = "docs/docs-graphql/schema.graphqls"
)

// Lint lints documentation files
func (d Docs) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("docs").Pipeline("lint")

	workdir := util.Repository(c)

	eg, gctx := errgroup.WithContext(ctx)

	// Markdown
	eg.Go(func() error {
		_, err = c.Container().
			From("tmknom/markdownlint:0.31.1").
			WithMountedDirectory("/src", workdir).
			WithMountedFile("/src/.markdownlint.yaml", workdir.File(".markdownlint.yaml")).
			WithWorkdir("/src").
			WithExec([]string{
				"-c",
				".markdownlint.yaml",
				"--",
				"./docs",
				"README.md",
			}).
			Sync(gctx)
		return err
	})

	eg.Go(func() error {
		return util.LintGeneratedCode(func() error {
			return d.Generate(ctx)
		}, generatedSchemaPath)
	})

	// Go is already linted by engine:lint
	// Python is already linted by sdk:python:lint
	// TypeScript is already linted at sdk:typescript:lint

	return eg.Wait()
}

func (d Docs) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("docs").Pipeline("generate-sdl")

	introspectionJSON :=
		util.GoBase(c).
			WithExec([]string{"go", "run", "./cmd/introspect"}, dagger.ContainerWithExecOpts{
				RedirectStdout: "introspection.json",
			}).
			File("introspection.json")

	_, err = c.Container().
		From("node:16-alpine").
		WithExec([]string{"npm", "install", "-g", "graphql-json-to-sdl"}).
		WithMountedFile("/src/schema.json", introspectionJSON).
		WithExec([]string{"graphql-json-to-sdl", "/src/schema.json", "/src/schema.graphql"}).
		File("/src/schema.graphql").
		Export(ctx, generatedSchemaPath)
	return err
}
