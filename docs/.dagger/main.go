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
)

func New(
	// +defaultPath="/"
	// +ignore=[
	// "bin",
	// ".git",
	// "**/node_modules",
	// "**/.venv",
	// "**/__pycache__",
	// "docs/node_modules",
	// "sdk/typescript/node_modules",
	// "sdk/typescript/dist",
	// "sdk/rust/examples/backend/target",
	// "sdk/rust/target",
	// "sdk/php/vendor"
	// ]
	source *dagger.Directory,
	// +defaultPath="nginx.conf"
	nginxConfig *dagger.File,
) Docs {
	return Docs{
		Source:      source,
		NginxConfig: nginxConfig,
	}
}

type Docs struct {
	Source      *dagger.Directory
	NginxConfig *dagger.File // +private
}

const (
	markdownlintVersion = "0.31.1"
)

const cliZenFrontmatter = `---
title: "CLI Reference"
description: "Learn how to use the Dagger CLI to run composable workflows in containers."
slug: "/reference/cli"
---

`

// Build the docs website
func (d Docs) Site() *dagger.Directory {
	opts := dagger.DocusaurusOpts{
		Dir:  "./docs",
		Yarn: true,
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
func (d Docs) Lint(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.Container().
		From("tmknom/markdownlint:"+markdownlintVersion).
		WithMountedDirectory("/src", d.Source).
		WithMountedFile("/src/.markdownlint.yaml", d.Source.File(".markdownlint.yaml")).
		WithMountedFile("/src/.markdownlintignore", d.Source.File("docs/.markdownlintignore")).
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
	return CheckCompleted, err
}

// Regenerate the API schema and CLI reference docs
func (d Docs) Generate(
	ctx context.Context,
	// Dagger version to generate API docs for
	// +optional
	version string,
) (*dagger.Changeset, error) {
	src := d.Source
	// 1. Generate the GraphQL schema
	withGqlSchema := dag.Go(dagger.GoOpts{Source: src}).Env().
		WithExec([]string{"go", "run", "./cmd/introspect", "--version=" + version, "schema"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "docs/docs-graphql/schema.graphqls",
		}).
		Directory(".")

	// 2. Generate the API reference docs
	withApiReference := dag.Container().
		From("node:22").
		WithMountedDirectory("/src", withGqlSchema).
		WithMountedDirectory("/mnt/spectaql", spectaql()).
		WithWorkdir("/src/docs").
		WithExec([]string{"yarn", "add", "file:/mnt/spectaql"}).
		// -t specifies the target directory where spectaql will write the generated output
		WithExec([]string{"yarn", "run", "spectaql", "./docs-graphql/config.yml", "-t", "./static/api/reference/"}).
		Directory("/src").
		WithoutDirectory("docs/node_modules").
		WithFile("docs/yarn.lock", src.File("docs/yarn.lock")).
		WithFile("docs/package.json", src.File("docs/package.json"))
	// 3. Generate CLI reference
	withCliReference := src.WithFile("docs/current_docs/reference/cli/index.mdx", dag.DaggerCli().Reference(
		dagger.DaggerCliReferenceOpts{
			Frontmatter:         cliZenFrontmatter,
			IncludeExperimental: true,
		},
	))
	// 4. Generate config file schemas?
	withConfigSchemas := dag.Go(dagger.GoOpts{Source: src}).Env().
		WithExec([]string{"go", "run", "./cmd/json-schema", "dagger.json"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "docs/static/reference/dagger.schema.json",
		}).
		WithExec([]string{"go", "run", "./cmd/json-schema", "engine.json"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "docs/static/reference/engine.schema.json",
		}).
		Directory(".")

	changes := src.
		WithChanges(withGqlSchema.Changes(src)).
		WithChanges(withApiReference.Changes(src)).
		WithChanges(withCliReference.Changes(src)).
		WithChanges(withConfigSchemas.Changes(src)).
		Changes(src)
	return changes, nil
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

func (d Docs) Introspection(
	version string, // +optional
) *dagger.File {
	return dag.
		Go(dagger.GoOpts{Source: d.Source}).
		Env().
		WithExec([]string{"go", "run", "./cmd/introspect", "--version=" + version, "introspect"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "introspection.json",
		}).
		File("introspection.json")
}

// Bump the Go SDK's Engine dependency
func (d Docs) Bump(version string) (*dagger.Changeset, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	versionFile := fmt.Sprintf(`// Code generated by dagger. DO NOT EDIT.

export const daggerVersion = "%s";
`, version)

	layer := d.Source.WithNewFile("docs/current_docs/partials/version.js", versionFile)
	return layer.Changes(d.Source), nil
}

// Deploys a current build of the docs.
// +cache="session"
func (d Docs) Deploy(
	ctx context.Context,
	message string,
	netlifyToken *dagger.Secret,
) (string, error) {
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
// +cache="session"
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
		// https://github.com/jedevc/spectaql/commit/174cde65e8457cea4f594a71686a1cfcd6042fd0
		WithMountedDirectory("/src", dag.Git("https://github.com/jedevc/spectaql").Commit("174cde65e8457cea4f594a71686a1cfcd6042fd0").Tree()).
		WithWorkdir("/src").
		WithExec([]string{"yarn", "install"}).
		WithExec([]string{"yarn", "run", "build"}).
		Directory("./")
}
