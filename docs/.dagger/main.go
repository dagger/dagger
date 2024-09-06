package main

import (
	"context"

	"go.opentelemetry.io/otel/codes"

	"dagger/docs/internal/dagger"
)

func New(
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!docs"]
	source *dagger.Directory,
) Docs {
	return Docs{
		Source: source,
	}
}

type Docs struct {
	Source *dagger.Directory // +private
}

// Build the docs website
func (docs Docs) Site() *dagger.Directory {
	return dag.
		Docusaurus(
			docs.Source,
			dagger.DocusaurusOpts{Dir: "/src/docs", DisableCache: true},
		).
		Build()
}

// Build the docs server
func (docs Docs) Server() *dagger.Container {
	nginxConfig := dag.CurrentModule().Source().File("nginx.conf")
	return dag.
		Container().
		From("nginx").
		WithoutEntrypoint().
		WithFile("/etc/nginx/conf.d/default.conf", nginxConfig).
		WithDefaultArgs([]string{"nginx", "-g", "daemon off;"}).
		WithDirectory("/var/www", docs.Site()).
		WithExposedPort(8000)
}

func withTrace(ctx context.Context, msg string, fn func(context.Context) error) error {
	ctx, span := Tracer().Start(ctx, msg)
	err := fn(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}

// Lint documentation files
func (docs Docs) Lint(
	ctx context.Context,
	// Re-generate all files before linting
	// +optional
	generate bool,
) error {
	source := docs.Source
	if generate {
		err := withTrace(ctx, "re-generate all docs files before linting",
			func(ctx context.Context) (err error) {
				source, err = docs.Generate(ctx, false)
				return
			})
		if err != nil {
			return err
		}
	}
	return withTrace(ctx, "lint markdown files", func(ctx context.Context) error {
		_, err := dag.Container().
			From("tmknom/markdownlint:0.31.1").
			WithMountedDirectory("/src", source).
			WithMountedFile("/src/.markdownlint.yaml", source.File(".markdownlint.yaml")).
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
}

// Regenerate the API schema and CLI reference docs
func (docs Docs) Generate(
	ctx context.Context,
	// Return an error if generated files were not up-to-date
	// +optional
	check bool,
) (*dagger.Directory, error) {
	generated := dirMerge([]*dagger.Directory{
		docs.GenerateCliReference(ctx),
		docs.GenerateApiReference(ctx),
		docs.GenerateApiSchema(ctx),
		docs.GenerateChangelog(ctx),
	})
	if check {
		return generated, dirAssertIsSubset(ctx, docs.Source, generated)
	}
	return generated, nil
}

// Generate the CLI reference documentation page
func (docs Docs) GenerateCliReference(ctx context.Context) *dagger.Directory {
	header, err := dag.CurrentModule().
		Source().
		File("cli-reference-header.mdx").
		Contents(ctx)
	if err != nil {
		panic(err) // missing source file = uncaught build error
	}
	reference := dag.DaggerCli().Reference(dagger.DaggerCliReferenceOpts{
		IncludeExperimental: true,
		Frontmatter:         header,
	})
	return dag.Directory().WithFile("docs/current_docs/reference/cli.mdx", reference)
}

// Generate the API GraphQL schema
func (docs Docs) GenerateApiSchema(_ context.Context) *dagger.Directory {
	introspectionJSON := dag.
		Go(docs.Source).
		Env().
		WithExec([]string{"go", "run", "./cmd/introspect"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "introspection.json",
		}).
		File("introspection.json")
	schema := dag.Graphql().FromJSON(introspectionJSON).File()
	return dag.Directory().WithFile("docs/docs-graphql/schema.graphqls", schema)
}

// Generate the API Reference documentation
func (docs Docs) GenerateApiReference(ctx context.Context) *dagger.Directory {
	reference := dag.Container().
		From("node:18").
		WithMountedDirectory("/src", docs.Source.WithDirectory(".", docs.GenerateApiSchema(ctx))).
		WithWorkdir("/src/docs").
		WithMountedDirectory("/mnt/spectaql", spectaql()).
		WithExec([]string{"yarn", "add", "file:/mnt/spectaql"}).
		WithExec([]string{"yarn", "run", "spectaql", "./docs-graphql/config.yml", "-t", "."}).
		File("index.html")
	return dag.Directory().WithFile("docs/static/api/reference/index.html", reference)
}

// Generate an up-do-date changelog, using changie
func (docs Docs) GenerateChangelog(ctx context.Context) *dagger.Directory {
	// FIXME: spin out a changie module
	changelog := dag.
		Container().
		From("ghcr.io/miniscruff/changie"). // FIXME: pin by digest
		WithMountedDirectory("/src", docs.Source).
		WithWorkdir("/src").
		WithExec([]string{"/changie", "merge"}).
		File("CHANGELOG.md")
	return dag.Directory().
		WithFiles("", []*dagger.File{changelog})
}

// Assert that inner directory is a subset of outer directory.
func dirAssertIsSubset(ctx context.Context, outer, inner *dagger.Directory) error {
	innerPaths, err := inner.Glob(ctx, "**")
	if err != nil {
		return err
	}
	return dag.Dirdiff().AssertEqual(ctx, outer, inner, innerPaths)
}

// Merge directories
func dirMerge(dirs []*dagger.Directory) *dagger.Directory {
	var out *dagger.Directory
	for _, dir := range dirs {
		if out == nil {
			out = dir
			continue
		}
		out = out.WithDirectory("/", dir)
	}
	return out
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
