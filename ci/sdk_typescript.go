package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/build"
	"github.com/dagger/dagger/ci/internal/dagger"
	"github.com/dagger/dagger/ci/util"
)

// TODO: use dev module (this is just the mage port)

const (
	typescriptRuntimeSubdir    = "sdk/typescript/runtime"
	typescriptGeneratedAPIPath = "sdk/typescript/api/client.gen.ts"

	nodeVersionMaintenance = "18"
	nodeVersionLTS         = "20"

	bunVersion = "1.0.27"
)

type TypescriptSDK struct {
	Dagger *Dagger // +private
}

// Lint the Typescript SDK
func (t TypescriptSDK) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)

	base := t.nodeJsBase()

	eg.Go(func() error {
		_, err := base.WithExec([]string{"yarn", "lint"}).Sync(ctx)
		return err
	})

	eg.Go(func() error {
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
	})

	eg.Go(func() error {
		return util.DiffDirectoryF(ctx, t.Dagger.Source, t.Generate, typescriptGeneratedAPIPath)
	})

	eg.Go(func() error {
		return lintGoModule(ctx, false, daggerDevelop(t.Dagger.Source, typescriptRuntimeSubdir), []string{typescriptRuntimeSubdir})
	})

	return eg.Wait()
}

// Test the Typescript SDK
func (t TypescriptSDK) Test(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk-typescript-test")
	if err != nil {
		return err
	}

	eg, ctx := errgroup.WithContext(ctx)

	// Loop over the LTS and Maintenance versions and test them
	for _, version := range []string{nodeVersionLTS, nodeVersionMaintenance} {
		base := t.nodeJsBaseFromVersion(version).With(installer)

		eg.Go(func() error {
			_, err := base.
				WithExec([]string{"yarn", "test:node"}).
				Sync(ctx)
			return err
		})
	}

	eg.Go(func() error {
		_, err = t.bunJsBase().
			With(installer).
			WithExec([]string{"bun", "test:bun"}).
			Sync(ctx)
		return err
	})

	return eg.Wait()
}

// Regenerate the Typescript SDK API
func (t TypescriptSDK) Generate(ctx context.Context) (*Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk-typescript-generate")
	if err != nil {
		return nil, err
	}
	build, err := build.NewBuilder(ctx, t.Dagger.Source)
	if err != nil {
		return nil, err
	}

	generated := t.nodeJsBase().
		With(installer).
		WithFile("/usr/local/bin/codegen", build.CodegenBinary()).
		WithExec([]string{"codegen", "--lang", "typescript", "-o", path.Dir(typescriptGeneratedAPIPath)}).
		WithExec([]string{"yarn", "fmt", typescriptGeneratedAPIPath}).
		File(typescriptGeneratedAPIPath)
	return dag.Directory().WithFile(typescriptGeneratedAPIPath, generated), nil
}

// Publish the Typescript SDK
func (t TypescriptSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,
	// +optional
	npmToken *Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/typescript/v")

	build := t.nodeJsBase().
		WithExec([]string{"npm", "run", "build"}).
		WithExec([]string{"npm", "version", version})
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

	_, err := publish.Sync(ctx)
	return err
}

// Bump the Typescript SDK's Engine dependency
func (t TypescriptSDK) Bump(version string) (*Directory, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	engineReference := fmt.Sprintf("// Code generated by dagger. DO NOT EDIT.\n"+
		"export const CLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return dag.Directory().WithNewFile("sdk/typescript/provisioning/default.ts", engineReference), nil
}

func (t TypescriptSDK) nodeJsBase() *Container {
	// Use the LTS version by default
	return t.nodeJsBaseFromVersion(nodeVersionMaintenance)
}

func (t TypescriptSDK) nodeJsBaseFromVersion(nodeVersion string) *Container {
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
		WithExec([]string{"yarn", "install"}).
		WithDirectory(mountPath, src)
}

func (t TypescriptSDK) bunJsBase() *Container {
	appDir := "sdk/typescript"
	src := t.Dagger.Source.Directory(appDir)

	// Mirror the same dir structure from the repo because of the
	// relative paths in eslint (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	return dag.Container().
		From("oven/bun:"+bunVersion).
		WithWorkdir(mountPath).
		WithMountedCache("/root/.bun/install/cache", dag.CacheVolume("bun_cache")).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithExec([]string{"bun", "install"}).
		WithDirectory(mountPath, src)
}
