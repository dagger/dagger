package main

import (
	"bytes"
	"context"
	"dagger/docs/internal/dagger"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/netlify/open-api/v2/go/models"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

func New(
	// +defaultPath="/"
	source *dagger.Directory,
	// +defaultPath="nginx.conf"
	nginxConfig *dagger.File,
	// +defaultPath="doctum-config.php"
	doctumConfig *dagger.File,
) Docs {
	return Docs{
		Source:       source,
		NginxConfig:  nginxConfig,
		DoctumConfig: doctumConfig,
	}
}

type Docs struct {
	Source       *dagger.Directory
	NginxConfig  *dagger.File // +private
	DoctumConfig *dagger.File // +private
}

const (
	generatedSchemaPath           = "docs/docs-graphql/schema.graphqls"
	generatedCliZenPath           = "docs/current_docs/reference/cli.mdx"
	generatedAPIReferencePath     = "docs/static/api/reference/index.html"
	generatedDaggerJSONSchemaPath = "docs/static/reference/dagger.schema.json"
	generatedEngineJSONSchemaPath = "docs/static/reference/engine.schema.json"
	generatedPhpReferencePath     = "docs/static/reference/php/"
)

const (
	doctumVersion       = "5.5.4"
	changieVersion      = "1.21.0"
	markdownlintVersion = "0.31.1"
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
	opts := dagger.DocusaurusOpts{
		Dir:  "/src/docs",
		Yarn: true,
		// HACK: cache seems to cause weird ephemeral errors occasionally -
		// probably because of cache sharing
		DisableCache: true,
	}
	return dag.Docusaurus(d.Source, opts).Build()
}

// Build the docs server
func (d Docs) Server() *dagger.Container {
	return dag.
		Container().
		From("nginx").
		WithoutEntrypoint().
		WithFile("/etc/nginx/conf.d/default.conf", d.NginxConfig).
		WithDefaultArgs([]string{"nginx", "-g", "daemon off;"}).
		WithDirectory("/var/www", d.Site()).
		WithExposedPort(8000)
}

// Lint documentation files
func (d Docs) Lint(ctx context.Context) (rerr error) {
	eg := errgroup.Group{}

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
			From("tmknom/markdownlint:"+markdownlintVersion).
			WithMountedDirectory("/src", d.Source).
			WithMountedFile("/src/.markdownlint.yaml", d.Source.File(".markdownlint.yaml")).
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
		before := d.Source
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
			generatedPhpReferencePath,
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
		before := d.Source
		// FIXME: spin out a changie module
		after := dag.
			Container().
			From("ghcr.io/miniscruff/changie:v"+changieVersion).
			WithMountedDirectory("/src", d.Source).
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
	dir := dag.Directory().
		WithDirectory("", d.GenerateSchema("")).
		WithDirectory("", d.GenerateSchemaReference("")).
		WithDirectory("", d.GenerateCli()).
		WithDirectory("", d.GenerateConfigSchemas())

	dirPHP, err := d.GeneratePhp(ctx)
	if err != nil {
		return nil, err
	}
	dir = dir.WithDirectory("", dirPHP)

	return dir, nil
}

// Regenerate the CLI reference docs
func (d Docs) GenerateCli() *dagger.Directory {
	generated := dag.DaggerCli().Reference(dagger.DaggerCliReferenceOpts{
		Frontmatter:         cliZenFrontmatter,
		IncludeExperimental: true,
	})
	return dag.Directory().WithFile(generatedCliZenPath, generated)
}

// Generate the PHP SDK API reference documentation
func (d Docs) GeneratePhp(ctx context.Context) (*dagger.Directory, error) {
	dir := dag.PhpSDKDev().Base().
		WithFile(
			"/usr/bin/doctum",
			dag.HTTP(fmt.Sprintf("https://doctum.long-term.support/releases/%s/doctum.phar", doctumVersion)),
			dagger.ContainerWithFileOpts{Permissions: 0711},
		).
		WithFile("/etc/doctum-config.php", d.DoctumConfig).
		WithExec([]string{"doctum", "update", "/etc/doctum-config.php", "-v"}).
		Directory("/src/sdk/php/build")

	// format this file, since otherwise it's on one line and makes lots of conflicts
	search, err := formatJSONFile(ctx, dir.File("doctum-search.json"))
	if err != nil {
		return nil, err
	}
	dir = dir.WithFile("doctum-search.json", search)

	// remove the renderer.index file, which seems to not be required to render the docs
	dir = dir.WithoutFile("renderer.index")

	return dag.Directory().WithDirectory(generatedPhpReferencePath, dir), nil
}

func formatJSONFile(ctx context.Context, f *dagger.File) (*dagger.File, error) {
	name, err := f.Name(ctx)
	if err != nil {
		return nil, err
	}

	contents, err := f.Contents(ctx)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	err = json.Indent(&out, []byte(contents), "", "\t")
	if err != nil {
		return nil, err
	}

	return dag.File(name, out.String()), nil
}

// Regenerate the API schema
func (d Docs) GenerateSchema(
	version string, // +optional
) *dagger.Directory {
	schema := dag.
		Go(d.Source).
		Env().
		WithExec([]string{"go", "run", "./cmd/introspect", "--version=" + version, "schema"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "schema.graphqls",
		}).
		File("schema.graphqls")
	return dag.Directory().WithFile(generatedSchemaPath, schema)
}

func (d Docs) Introspection(
	version string, // +optional
) *dagger.File {
	return dag.
		Go(d.Source).
		Env().
		WithExec([]string{"go", "run", "./cmd/introspect", "--version=" + version, "introspect"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "introspection.json",
		}).
		File("introspection.json")
}

// Regenerate the API Reference documentation
func (d Docs) GenerateSchemaReference(
	version string, // +optional
) *dagger.Directory {
	generatedHTML := dag.Container().
		From("node:18").
		WithMountedDirectory("/src", d.Source.WithDirectory(".", d.GenerateSchema(version))).
		WithWorkdir("/src/docs").
		WithMountedDirectory("/mnt/spectaql", spectaql()).
		WithExec([]string{"yarn", "add", "file:/mnt/spectaql"}).
		WithExec([]string{"yarn", "run", "spectaql", "./docs-graphql/config.yml", "-t", "."}).
		File("index.html")
	return dag.Directory().WithFile(generatedAPIReferencePath, generatedHTML)
}

// Regenerate the config schemas
func (d Docs) GenerateConfigSchemas() *dagger.Directory {
	ctr := dag.Go(d.Source).Env()

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

// Deploys a current build of the docs.
func (d Docs) Deploy(
	ctx context.Context,
	netlifyToken *dagger.Secret,
) (string, error) {
	commit, err := dag.Version().Git().Head().Commit(ctx)
	if err != nil {
		return "", err
	}
	dirty, err := dag.Version().Git().Dirty(ctx)
	if err != nil {
		return "", err
	}
	message := "Manual build on " + commit
	if dirty {
		message += "-dirty"
	}

	out, err := dag.Container().
		From("node:18").
		WithExec([]string{"npm", "install", "netlify-cli", "-g"}). // pin!!!!
		WithEnvVariable("NETLIFY_SITE_ID", "docs-dagger-io").
		WithSecretVariable("NETLIFY_AUTH_TOKEN", netlifyToken).
		WithMountedDirectory("/build", d.Site()).
		WithExec([]string{"netlify", "deploy", "--dir=/build", "--branch=main", "--message", message, "--json"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	var dt struct {
		DeployID string `json:"deploy_id"`
	}
	if err := json.Unmarshal([]byte(out), &dt); err != nil {
		return "", err
	}

	return dt.DeployID, nil
}

// Publish a previous deployment to production - defaults to the latest deployment on the main branch.
func (d Docs) Publish(
	ctx context.Context,
	netlifyToken *dagger.Secret,
	// +optional
	deployment string,
) error {
	api := "https://api.netlify.com/api/v1"
	site := "docs.dagger.io"
	branch := "main"
	client := http.Client{}

	token, err := netlifyToken.Plaintext(ctx)
	if err != nil {
		return err
	}

	if deployment == "" {
		// get all the deploys for "main", ordered by most recent
		url := fmt.Sprintf("%s/sites/%s/deploys?branch=%s", api, site, branch)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Add("Authorization", "Bearer "+token)
		result, err := client.Do(req)
		if err != nil {
			return err
		}
		defer result.Body.Close()
		if result.StatusCode != 200 {
			return fmt.Errorf("unexpected status code while listing deploys %s %d", url, result.StatusCode)
		}
		data, err := io.ReadAll(result.Body)
		if err != nil {
			return err
		}
		var deploys []models.Deploy
		err = json.Unmarshal(data, &deploys)
		if err != nil {
			return err
		}
		if len(deploys) == 0 {
			return fmt.Errorf("no deploys for %q", site)
		}

		deployment = deploys[0].ID
	}

	// publish the most recent deploy
	// NOTE: this is called "restore", which is mildly confusing, but it's also
	// exactly what the web ui does :P
	url := fmt.Sprintf("%s/sites/%s/deploys/%s/restore", api, site, deployment)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+token)
	result, err := client.Do(req)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode != 200 {
		return fmt.Errorf("unexpected status code while restoring deploy %s %d", url, result.StatusCode)
	}

	return nil
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
