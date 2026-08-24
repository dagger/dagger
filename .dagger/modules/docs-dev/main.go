// Dagger docs toolchain
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"dagger/docs/internal/dagger"

	"github.com/netlify/open-api/v2/go/models"
)

func New(
	// +defaultPath="/"
	// +ignore=[
	// "*",
	// "**/node_modules",
	// "!docs",
	// "!sdk/typescript",
	// "!CONTRIBUTING.md"
	// ]
	source *dagger.Directory,
	// +defaultPath="/docs/nginx.conf"
	nginxConfig *dagger.File,
) DocsDev {
	return DocsDev{
		Source:      source,
		NginxConfig: nginxConfig,
	}
}

type DocsDev struct {
	Source      *dagger.Directory
	NginxConfig *dagger.File // +private
}

// Build the docs website
func (d DocsDev) Site() *dagger.Directory {
	opts := dagger.DocusaurusOpts{
		Dir:  "./docs",
		Yarn: true,
	}
	return dag.Docusaurus(d.Source, opts).Build()
}

// Freeze a released version's docs as a versioned snapshot. The docs are pulled
// from the version's git tag -- not the in-development docs on the current
// branch -- so the snapshot reflects what actually shipped. Runs docusaurus
// docs:version and returns a Changeset that adds
// versioned_docs/version-<version>/ (plus its sidebar) and prepends the version
// to docs/versions.json, for review before applying.
func (d DocsDev) SnapshotRelease(
	// Release version to snapshot, e.g. v1.0.0-beta.10. A leading "v" is dropped
	// so the snapshot name matches docs/versions.json (bare semver).
	version string,
) (*dagger.Changeset, error) {
	version = strings.TrimPrefix(version, "v")
	// The released docs, straight from the tag.
	tagDocs := dag.Git("https://github.com/dagger/dagger").
		Tag("v" + version).
		Tree().
		Directory("docs")
	// Run docs:version against the current docs tree, but with current_docs and
	// its sidebar swapped for the release's, so the snapshot captures the
	// release rather than whatever is in development on this branch.
	opts := dagger.DocusaurusOpts{
		Dir:  "./docs",
		Yarn: true,
	}
	built := dag.Docusaurus(
		d.Source.
			WithDirectory("docs/current_docs", tagDocs.Directory("current_docs")).
			WithFile("docs/sidebars.ts", tagDocs.File("sidebars.ts")),
		opts,
	).
		Base().
		WithExec([]string{"yarn", "docusaurus", "docs:version", version}).
		Directory("/src")
	// Keep only the new snapshot artifacts; don't leak the swapped current_docs
	// or sidebar into the changeset.
	versionDir := "docs/versioned_docs/version-" + version
	sidebar := "docs/versioned_sidebars/version-" + version + "-sidebars.json"
	return d.Source.
		WithDirectory(versionDir, built.Directory(versionDir)).
		WithFile(sidebar, built.File(sidebar)).
		WithFile("docs/versions.json", built.File("docs/versions.json")).
		Changes(d.Source), nil
}

// Check the docs website build
// +check
func (d DocsDev) Check(ctx context.Context) error {
	_, err := d.Site().Sync(ctx)
	return err
}

// Build the docs server
func (d DocsDev) Server() *dagger.Container {
	return dag.
		Container().
		From("nginx").
		WithoutEntrypoint().
		WithFile("/etc/nginx/conf.d/default.conf", d.NginxConfig).
		WithDefaultArgs([]string{"nginx", "-g", "daemon off;"}).
		WithDirectory("/var/www", d.Site()).
		WithExposedPort(8000)
}

// Regenerate the API schema and CLI reference docs
// +generate
func (d DocsDev) References(
	// Dagger version to generate API docs for
	// +optional
	version string,

	// Workspace forwarded to engine-dev for VCS stamping (References is the
	// only docs-dev method that builds). Auto-injected on a direct call;
	// dependencies don't inherit it.
	// +optional
	ws *dagger.Workspace,
) (*dagger.Changeset, error) {
	src := d.Source
	// 1. Generate the GraphQL schema
	withGqlSchema := src.WithFile(
		"docs/docs-graphql/schema.graphqls",
		dag.EngineDev(ws).GraphqlSchema(dagger.EngineDevGraphqlSchemaOpts{
			Version: version,
		}),
	)
	// 2. Generate the API reference stubs.
	//
	// The reference pages under docs/current_docs/api/reference are rendered
	// from docs-graphql/schema.graphqls at site-build time by the
	// dagger-api-reference Docusaurus plugin (see docs/plugins and
	// docs/src/components/api). All this step regenerates is the thin per-type
	// MDX stubs, so they stay in sync with the published core-type list.
	opts := dagger.DocusaurusOpts{
		Dir:  "./docs",
		Yarn: true,
	}
	withAPIReference := dag.Docusaurus(withGqlSchema, opts).
		Base().
		WithExec([]string{"node", "plugins/dagger-api-reference/generate-stubs.js"}).
		Directory("/src").
		WithoutDirectory("docs/node_modules")
	// The CLI reference (docs/current_docs/cli/reference/index.mdx) is generated
	// separately by the go toolchain (see docs/current_docs/cli/generate.go)
	// and committed, so it is already part of src here.

	// 3. Generate config file schemas?
	withConfigSchemas := src.
		WithFile("docs/static/reference/dagger.schema.json", dag.EngineDev(ws).ConfigSchema("dagger.json")).
		WithFile("docs/static/reference/dagger-module.schema.json", dag.EngineDev(ws).ConfigSchema("dagger-module.toml")).
		WithFile("docs/static/reference/dagger-workspace.schema.json", dag.EngineDev(ws).ConfigSchema("dagger.toml"))

	changes := src.
		WithChanges(withGqlSchema.Changes(src)).
		WithChanges(withAPIReference.Changes(src)).
		WithChanges(withConfigSchemas.Changes(src)).
		Changes(src)
	return changes, nil
}

// Deploys a current build of the docs.
// +cache="session"
func (d DocsDev) Deploy(
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
func (d DocsDev) Publish(
	ctx context.Context,
	netlifyToken *dagger.Secret,
	// +optional
	deployment string,
	// +optional
	apiURL string,
) error {
	api := "https://api.netlify.com/api/v1"
	if apiURL != "" {
		api = strings.TrimRight(apiURL, "/")
	}
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
