package main

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/internal/dagger"
	"github.com/dagger/dagger/ci/util"
)

type Docs struct {
	Dagger *Dagger // +private
}

const (
	generatedSchemaPath = "docs/docs-graphql/schema.graphqls"
	generatedCliZenPath = "docs/current_docs/reference/979596-cli.mdx"
)

const cliZenFrontmatter = `---
slug: /reference/979596/cli/
pagination_next: null
pagination_prev: null
---

# CLI Reference
`

// Lint documentation files
func (d Docs) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)

	// Markdown
	eg.Go(func() error {
		_, err := dag.Container().
			From("tmknom/markdownlint:0.31.1").
			WithMountedDirectory("/src", d.Dagger.Source).
			WithMountedFile("/src/.markdownlint.yaml", d.Dagger.Source.File(".markdownlint.yaml")).
			WithWorkdir("/src").
			WithExec([]string{
				"-c",
				".markdownlint.yaml",
				"--",
				"./docs",
				"README.md",
			}).
			Sync(ctx)
		return err
	})

	eg.Go(func() error {
		return util.DiffDirectoryF(ctx, d.Dagger.Source, d.Generate, generatedSchemaPath, generatedCliZenPath)
	})

	eg.Go(func() error {
		return util.DiffDirectoryF(ctx, d.Dagger.Source, func(ctx context.Context) (*dagger.Directory, error) {
			return dag.Container().
				From("ghcr.io/miniscruff/changie").
				WithMountedDirectory("/src", d.Dagger.Source).
				WithWorkdir("/src").
				WithExec([]string{"merge"}).
				Directory("/src"), nil
		}, "CHANGELOG.md")
	})

	// Go is already linted by engine:lint
	// Python is already linted by sdk:python:lint
	// TypeScript is already linted at sdk:typescript:lint

	return eg.Wait()
}

// Regenerate the API schema and CLI reference docs
func (d Docs) Generate(ctx context.Context) (*dagger.Directory, error) {
	eg, ctx := errgroup.WithContext(ctx)
	_ = ctx

	var sdl *dagger.Directory
	eg.Go(func() error {
		sdl = d.GenerateSdl()
		return nil
	})
	var cli *dagger.Directory
	eg.Go(func() error {
		cli = d.GenerateCli()
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return sdl.WithDirectory("/", cli), nil
}

// Regenerate the API schema
func (d Docs) GenerateSdl() *Directory {
	introspectionJSON :=
		util.GoBase(d.Dagger.Source).
			WithExec([]string{"go", "run", "./cmd/introspect"}, dagger.ContainerWithExecOpts{
				RedirectStdout: "introspection.json",
			}).
			File("introspection.json")

	generated := dag.Container().
		From("node:16-alpine").
		WithExec([]string{"npm", "install", "-g", "graphql-json-to-sdl"}).
		WithMountedFile("/src/schema.json", introspectionJSON).
		WithExec([]string{"graphql-json-to-sdl", "/src/schema.json", "/src/schema.graphql"}).
		File("/src/schema.graphql")

	return dag.Directory().WithFile(generatedSchemaPath, generated)
}

// Regenerate the CLI reference docs
func (d Docs) GenerateCli() *Directory {
	// Should we keep `--include-experimental`?
	generated := util.GoBase(d.Dagger.Source).
		WithExec([]string{"go", "run", "./cmd/dagger", "gen", "--frontmatter=" + cliZenFrontmatter, "--output=cli.mdx", "--include-experimental"}).
		File("cli.mdx")
	return dag.Directory().WithFile(generatedCliZenPath, generated)
}