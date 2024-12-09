package main

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type Docs struct {
	Dagger *DaggerDev // +private
}

const (
	generatedSchemaPath           = "docs/docs-graphql/schema.graphqls"
	generatedCliZenPath           = "docs/current_docs/reference/cli.mdx"
	generatedAPIReferencePath     = "docs/static/api/reference/index.html"
	generatedDaggerJSONSchemaPath = "docs/static/reference/dagger.schema.json"
	generatedEngineJSONSchemaPath = "docs/static/reference/engine.schema.json"
)

const cliZenFrontmatter = `---
slug: /reference/cli/
pagination_next: null
pagination_prev: null
---

# CLI Reference
`

// Build the docs website
func (d Docs) Site() *dagger.Directory {
	return dag.
		Docusaurus(
			d.Dagger.Source(),
			dagger.DocusaurusOpts{
				Dir:  "/src/docs",
				Yarn: true,
				// HACK: cache seems to cause weird ephemeral errors occasionally -
				// probably because of cache sharing
				DisableCache: true,
			},
		).
		Build()
}

// Build the docs server
func (d Docs) Server() *dagger.Container {
	nginxConfig := dag.CurrentModule().Source().File("docs-nginx.conf")
	return dag.
		Container().
		From("nginx").
		WithoutEntrypoint().
		WithFile("/etc/nginx/conf.d/default.conf", nginxConfig).
		WithDefaultArgs([]string{"nginx", "-g", "daemon off;"}).
		WithDirectory("/var/www", d.Site()).
		WithExposedPort(8000)
}

// Lint documentation files
func (d Docs) Lint(ctx context.Context) (rerr error) {
	eg, ctx := errgroup.WithContext(ctx)

	// Markdown
	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint markdown files")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		_, err := dag.Container().
			From("tmknom/markdownlint:0.31.1").
			WithMountedDirectory("/src", d.Dagger.Source()).
			WithMountedFile("/src/.markdownlint.yaml", d.Dagger.Source().File(".markdownlint.yaml")).
			WithWorkdir("/src").
			WithExec([]string{
				"markdownlint",
				"-c",
				".markdownlint.yaml",
				"--",
				"./docs",
				"README.md",
			}).
			Sync(ctx)
		return err
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that generated docs are up-to-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := d.Dagger.Source()
		after, err := d.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{
			generatedSchemaPath,
			generatedCliZenPath,
			generatedAPIReferencePath,
			generatedDaggerJSONSchemaPath,
			generatedEngineJSONSchemaPath,
		})
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that changelog is up-do-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := d.Dagger.Source()
		// FIXME: spin out a changie module
		after := dag.
			Container().
			From("ghcr.io/miniscruff/changie").
			WithMountedDirectory("/src", d.Dagger.Source()).
			WithWorkdir("/src").
			WithExec([]string{"/changie", "merge"}).
			Directory("/src")
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{"CHANGELOG.md"})
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that site builds")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		_, err := d.Site().Sync(ctx)
		return err
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
		sdl = d.GenerateSchema()
		return nil
	})
	var cli *dagger.Directory
	eg.Go(func() error {
		cli = d.GenerateCli()
		return nil
	})
	var apiRef *dagger.Directory
	eg.Go(func() error {
		apiRef = d.GenerateSchemaReference()
		return nil
	})
	var configSchemas *dagger.Directory
	eg.Go(func() error {
		configSchemas = d.GenerateConfigSchemas()
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	result := dag.Directory().
		WithDirectory("", sdl).
		WithDirectory("", cli).
		WithDirectory("", apiRef).
		WithDirectory("", configSchemas)
	return result, nil
}

// Regenerate the CLI reference docs
func (d Docs) GenerateCli() *dagger.Directory {
	generated := dag.DaggerCli().Reference(dagger.DaggerCliReferenceOpts{
		Frontmatter:         cliZenFrontmatter,
		IncludeExperimental: true,
	})
	return dag.Directory().WithFile(generatedCliZenPath, generated)
}

// Regenerate the API schema
func (d Docs) GenerateSchema() *dagger.Directory {
	introspectionJSON := dag.
		Go(d.Dagger.Source()).
		Env().
		WithExec([]string{"go", "run", "./cmd/introspect"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "introspection.json",
		}).
		File("introspection.json")
	return dag.
		Directory().
		WithFile(generatedSchemaPath, dag.Graphql().FromJSON(introspectionJSON).File())
}

// Regenerate the API Reference documentation
func (d Docs) GenerateSchemaReference() *dagger.Directory {
	generatedHTML := dag.Container().
		From("node:18").
		WithMountedDirectory("/src", d.Dagger.Source().WithDirectory(".", d.GenerateSchema())).
		WithWorkdir("/src/docs").
		WithMountedDirectory("/mnt/spectaql", spectaql()).
		WithExec([]string{"yarn", "add", "file:/mnt/spectaql"}).
		WithExec([]string{"yarn", "run", "spectaql", "./docs-graphql/config.yml", "-t", "."}).
		File("index.html")
	return dag.Directory().WithFile(generatedAPIReferencePath, generatedHTML)
}

// Regenerate the config schemas
func (d Docs) GenerateConfigSchemas() *dagger.Directory {
	ctr := dag.Go(d.Dagger.Source()).Env()

	daggerJSONSchema := ctr.
		WithExec([]string{"go", "run", "./cmd/json-schema", "dagger.json"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "dagger.schema.json",
		}).
		File("dagger.schema.json")
	engineJSONSchema := ctr.
		WithExec([]string{"go", "run", "./cmd/json-schema", "engine.json"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "engine.schema.json",
		}).
		File("engine.schema.json")
	return dag.
		Directory().
		WithFile(generatedDaggerJSONSchemaPath, daggerJSONSchema).
		WithFile(generatedEngineJSONSchemaPath, engineJSONSchema)
}

// Bump the Go SDK's Engine dependency
func (d Docs) Bump(version string) (*dagger.Directory, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	versionFile := fmt.Sprintf(`// Code generated by dagger. DO NOT EDIT.

export const daggerVersion = "%s";
`, version)

	dir := dag.Directory().WithNewFile("docs/current_docs/partials/version.js", versionFile)
	return dir, nil
}

func spectaql() *dagger.Directory {
	// HACK: return a custom build of spectaql that has reproducible example
	// snippets (can be removed if anvilco/spectaql#976 is merged and released)
	return dag.Container().
		From("node:18").
		// https://github.com/jedevc/spectaql/commit/1a59bf93c6ff0d13195eea98e5d2d27cd2ee8fc7
		WithMountedDirectory("/src", dag.Git("https://github.com/jedevc/spectaql").Commit("1a59bf93c6ff0d13195eea98e5d2d27cd2ee8fc7").Tree()).
		WithWorkdir("/src").
		WithExec([]string{"yarn", "install"}).
		WithExec([]string{"yarn", "run", "build"}).
		Directory("./")
}
