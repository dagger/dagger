package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// TODO: use dev module (this is just the mage port)

const (
	nodePreviousLTS = "20.18.1"
	nodeCurrentLTS  = "22.11.0"

	bunVersion = "1.1.38"
)

type TypescriptSDK struct {
	Dagger *DaggerDev // +private
}

func (t TypescriptSDK) Name() string {
	return "typescript"
}

// Lint the Typescript SDK
func (t TypescriptSDK) CheckLint(ctx context.Context) (rerr error) {
	return parallel.New().
		WithJob("check typescript format", t.CheckLintTypescript).
		WithJob("check docs snippets format", t.CheckLintSnippets).
		WithJob("lint the typescript runtime, which is written in Go", t.CheckGoLint).
		Run(ctx)
}

// CheckTypescriptFormat checks the formatting of the Typescript SDK code
func (t TypescriptSDK) CheckLintTypescript(ctx context.Context) error {
	base := t.nodeJsBase()
	_, err := base.WithExec([]string{"yarn", "lint"}).Sync(ctx)
	return err
}

// CheckDocsSnippetsFormat checks the formatting of Typescript snippets in the docs
func (t TypescriptSDK) CheckLintSnippets(ctx context.Context) error {
	base := t.nodeJsBase()
	path := "docs/current_docs"
	_, err := base.
		WithDirectory(
			fmt.Sprintf("/%s", path),
			t.Dagger.Source.Directory(path),
			dagger.ContainerWithDirectoryOpts{
				Include: []string{
					"**/*.mts",
					"**/*.mjs",
					"**/*.ts",
					"**/*.js",
					"*prettier*",
					"*eslint*",
				},
			},
		).
		WithExec([]string{"yarn", "docs:lint"}).
		Sync(ctx)
	return err
}

// CheckGoFormat checks the formatting of the typescript runtime, which is written in Go
func (t TypescriptSDK) CheckGoLint(ctx context.Context) (rerr error) {
	return t.godev().CheckLint(ctx)
}

func (t TypescriptSDK) godev() *dagger.Go {
	return dag.Go(t.RuntimeSource())
}

func (t TypescriptSDK) RuntimeSource() *dagger.Directory {
	return t.Dagger.Source.Filter(dagger.DirectoryFilterOpts{
		Include: []string{"sdk/typescript/runtime"},
	})
}

// Test the Typescript SDK
func (t TypescriptSDK) Test(ctx context.Context) (rerr error) {
	jobs := parallel.New()
	// Loop over the LTS and Maintenance versions and test them
	for _, version := range []string{nodeCurrentLTS, nodePreviousLTS} {
		base := t.
			nodeJsBaseFromVersion(version).
			With(t.Dagger.devEngineSidecar())
		jobs = jobs.WithJob(
			fmt.Sprintf("test with node version %s", version),
			func(ctx context.Context) error {
				_, err := base.
					WithExec([]string{"yarn", "test:node", "-i", "-g", "Automatic Provisioned CLI Binary"}).
					Sync(ctx)
				return err
			},
		)
	}
	jobs = jobs.WithJob(
		fmt.Sprintf("test with bun version %s", bunVersion),
		func(ctx context.Context) error {
			_, err := t.bunJsBase().
				With(t.Dagger.devEngineSidecar()).
				WithExec([]string{"bun", "test:bun", "-i", "-g", "Automatic Provisioned CLI Binary"}).
				Sync(ctx)
			return err
		},
	)
	return jobs.Run(ctx)
}

// Regenerate the Typescript client bindings
func (t TypescriptSDK) Generate(ctx context.Context) (*dagger.Changeset, error) {
	return t.NodeJsContainer("").
		WithMountedDirectory(".", t.Dagger.Source).
		WithExec([]string{"yarn", "--cwd", "sdk/typescript", "install"}).
		With(t.Dagger.devEngineSidecar()).
		WithFile("/usr/local/bin/codegen", t.Dagger.codegenBinary()).
		WithExec([]string{
			"codegen", "generate-library", "--lang", "typescript", "-o", "./sdk/typescript/src/api/",
		}).
		WithExec([]string{
			"yarn", "--cwd", "sdk/typescript", "eslint", "--max-warnings=0", "--fix", "./src/api/",
		}).
		Directory(".").
		// FIXME: more efficient way to exclude node_modules from the diff?
		WithoutDirectory("sdk/typescript/node_modules").
		// FIXME: since we know this is purely additive, compare to empty dir for more efficient diff?
		Changes(t.Dagger.Source).Sync(ctx)
}

// Test the publishing process
func (t TypescriptSDK) CheckReleaseDryRun(ctx context.Context) error {
	return t.Publish(ctx, "HEAD", true, nil)
}

// Publish the Typescript SDK
func (t TypescriptSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,
	// +optional
	npmToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/typescript/")
	versionFlag := strings.TrimPrefix(version, "v")
	if !semver.IsValid(version) {
		versionFlag = "prepatch"
	}

	build := t.nodeJsBase().
		WithExec([]string{"npm", "run", "build"}).
		WithExec([]string{"npm", "version", versionFlag})

	_, err := build.Directory("dist").Entries(ctx)
	if err != nil {
		return errors.New("dist directory does not exist")
	}

	if !dryRun {
		plaintext, err := npmToken.Plaintext(ctx)
		if err != nil {
			return err
		}
		npmrc := fmt.Sprintf(`//registry.npmjs.org/:_authToken=%s
registry=https://registry.npmjs.org/
always-auth=true`, plaintext)
		build = build.WithMountedSecret(".npmrc", dag.SetSecret("npmrc", npmrc))
	}

	publish := build.WithExec([]string{"npm", "publish", "--access", "public"})
	if dryRun {
		publish = build.WithExec([]string{"npm", "publish", "--access", "public", "--dry-run"})
	}
	_, err = publish.Sync(ctx)
	if err != nil {
		return err
	}

	return nil
}

// Bump the Typescript SDK's Engine dependency
func (t TypescriptSDK) Bump(_ context.Context, version string) (*dagger.Changeset, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	engineReference := fmt.Sprintf("// Code generated by dagger. DO NOT EDIT.\n"+
		"export const CLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	layer := t.Dagger.Source.WithNewFile("sdk/typescript/src/provisioning/default.ts", engineReference)
	return layer.Changes(t.Dagger.Source), nil
}

func (t TypescriptSDK) nodeJsBase() *dagger.Container {
	// Use the LTS version by default
	return t.nodeJsBaseFromVersion(nodePreviousLTS)
}

// Return an actual nodejs base image
// A base image does not have application source code mounted
// This allows cleaner control over mounting later, for example
// for the purposes of generating changes before/after running the container
func (t TypescriptSDK) NodeJsContainer(
	// +optional
	version string,
) *dagger.Container {
	if version == "" {
		version = nodePreviousLTS
	}
	return dag.Container().
		From("node:"+version+"-alpine").
		WithMountedCache("/usr/local/share/.cache/yarn", dag.CacheVolume("yarn_cache:"+version)).
		WithWorkdir("/app")
}

func (t TypescriptSDK) nodeJsBaseFromVersion(nodeVersion string) *dagger.Container {
	appDir := "sdk/typescript"
	src := t.Dagger.Source.Directory(appDir)

	// Mirror the same dir structure from the repo because of the
	// relative paths in eslint (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	nodeVersionImage := fmt.Sprintf("node:%s-alpine", nodeVersion)

	return dag.Container().
		// ⚠️  Keep this in sync with the engine version defined in package.json
		From(nodeVersionImage).
		WithWorkdir(mountPath).
		WithMountedCache("/usr/local/share/.cache/yarn", dag.CacheVolume(fmt.Sprintf("yarn_cache:%s", nodeVersion))).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithFile(fmt.Sprintf("%s/yarn.lock", mountPath), src.File("yarn.lock")).
		WithFile(fmt.Sprintf("%s/eslint.config.js", mountPath), src.File("eslint.config.js")).
		WithExec([]string{"yarn", "install"}).
		WithDirectory(mountPath, src)
}

func (t TypescriptSDK) bunJsBase() *dagger.Container {
	appDir := "sdk/typescript"
	src := t.Dagger.Source.Directory(appDir)

	// Mirror the same dir structure from the repo because of the
	// relative paths in eslint (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	return dag.Container().
		From("oven/bun:"+bunVersion+"-alpine").
		WithWorkdir(mountPath).
		WithMountedCache("/root/.bun/install/cache", dag.CacheVolume("bun_cache")).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithExec([]string{"bun", "install"}).
		WithDirectory(mountPath, src)
}
