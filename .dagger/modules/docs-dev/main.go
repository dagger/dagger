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
	"dagger/docs/internal/releaseversion"

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

// Generate a docs version from a Git ref. Numbered prereleases and patch
// components are collapsed into rolling channels by default.
func (d DocsDev) GenerateVersion(
	ctx context.Context,
	// Git ref whose docs will populate the version, e.g.
	// https://github.com/dagger/dagger#v1.0.0-beta.10.
	source *dagger.GitRef,
	// Exact destination docs version. Allows a branch or commit source and
	// replaces an existing version; collapse options are ignored when set.
	// +optional
	as string,
	// Collapse a trailing numeric prerelease identifier, e.g.
	// 1.0.0-beta.10 to 1.0.0-beta.
	// +optional
	// +default=true
	collapsePreReleases bool,
	// Collapse the patch component, e.g. 0.21.8 to 0.21.
	// +optional
	// +default=true
	collapsePatch bool,
) (*dagger.Changeset, error) {
	ref, err := source.Ref(ctx)
	if err != nil {
		return nil, fmt.Errorf("get source ref name: %w", err)
	}
	version, rolling, err := releaseversion.Resolve(ref, as, collapsePreReleases, collapsePatch)
	if err != nil {
		return nil, err
	}

	versionDir := "docs/versioned_docs/version-" + version
	sidebar := "docs/versioned_sidebars/version-" + version + "-sidebars.json"
	exists, err := d.Source.Exists(ctx, versionDir)
	if err != nil {
		return nil, fmt.Errorf("check for existing docs snapshot: %w", err)
	}
	if exists && !rolling {
		return d.Source.Changes(d.Source), nil
	}
	generatedVersion := version
	if exists {
		// Docusaurus refuses to generate an existing version. Generate under the
		// temporary name, then install it at the rolling channel below.
		generatedVersion = "docs-tmp"
	}

	// The source docs, straight from the supplied ref.
	sourceDocs := source.Tree().Directory("docs")
	// Run docs:version against the current docs tree, but with current_docs and
	// its sidebar swapped for the source's, so the snapshot captures the source
	// rather than whatever is in development on this branch.
	opts := dagger.DocusaurusOpts{
		Dir:  "./docs",
		Yarn: true,
	}
	built := dag.Docusaurus(
		d.Source.
			WithoutDirectory("docs/current_docs").
			WithDirectory("docs/current_docs", sourceDocs.Directory("current_docs")).
			WithFile("docs/sidebars.ts", sourceDocs.File("sidebars.ts")),
		opts,
	).
		Base().
		WithExec([]string{"yarn", "docusaurus", "docs:version", generatedVersion}).
		Directory("/src")
	// Keep only the new snapshot artifacts; don't leak the swapped current_docs
	// or sidebar into the changeset.
	generatedDir := "docs/versioned_docs/version-" + generatedVersion
	generatedSidebar := "docs/versioned_sidebars/version-" + generatedVersion + "-sidebars.json"
	result := d.Source
	if exists {
		// Replace rather than merge so removed pages do not survive an update.
		result = result.WithoutDirectory(versionDir)
	}
	result = result.
		WithDirectory(versionDir, built.Directory(generatedDir)).
		WithFile(sidebar, built.File(generatedSidebar))
	if !exists {
		contents, err := built.File("docs/versions.json").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("read docs versions: %w", err)
		}
		var versions []string
		if err := json.Unmarshal([]byte(contents), &versions); err != nil {
			return nil, fmt.Errorf("parse docs versions: %w", err)
		}
		versions, err = releaseversion.SortNewestFirst(versions)
		if err != nil {
			return nil, fmt.Errorf("sort docs versions: %w", err)
		}
		contentsJSON, err := json.MarshalIndent(versions, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal docs versions: %w", err)
		}
		result = result.WithNewFile("docs/versions.json", string(contentsJSON)+"\n")
	}
	return result.Changes(d.Source), nil
}

// Rename an existing docs version without changing its contents.
func (d DocsDev) RenameVersion(
	ctx context.Context,
	// Existing docs version.
	from string,
	// New docs version.
	to string,
) (*dagger.Changeset, error) {
	contents, err := d.Source.File("docs/versions.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read docs versions: %w", err)
	}
	var versions []string
	if err := json.Unmarshal([]byte(contents), &versions); err != nil {
		return nil, fmt.Errorf("parse docs versions: %w", err)
	}
	versions, err = releaseversion.Rename(versions, from, to)
	if err != nil {
		return nil, err
	}

	fromDir := "docs/versioned_docs/version-" + from
	toDir := "docs/versioned_docs/version-" + to
	fromSidebar := "docs/versioned_sidebars/version-" + from + "-sidebars.json"
	toSidebar := "docs/versioned_sidebars/version-" + to + "-sidebars.json"
	for _, artifact := range []struct {
		path   string
		exists bool
	}{
		{path: fromDir, exists: true},
		{path: fromSidebar, exists: true},
		{path: toDir, exists: false},
		{path: toSidebar, exists: false},
	} {
		exists, err := d.Source.Exists(ctx, artifact.path)
		if err != nil {
			return nil, fmt.Errorf("check docs version artifact %q: %w", artifact.path, err)
		}
		if exists != artifact.exists {
			if artifact.exists {
				return nil, fmt.Errorf("docs version artifact %q does not exist", artifact.path)
			}
			return nil, fmt.Errorf("docs version artifact %q already exists", artifact.path)
		}
	}

	contentsJSON, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal docs versions: %w", err)
	}
	result := d.Source.
		WithDirectory(toDir, d.Source.Directory(fromDir)).
		WithFile(toSidebar, d.Source.File(fromSidebar)).
		WithoutDirectory(fromDir).
		WithoutFile(fromSidebar).
		WithNewFile("docs/versions.json", string(contentsJSON)+"\n")
	return result.Changes(d.Source), nil
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
