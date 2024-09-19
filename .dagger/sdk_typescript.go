package main

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// TODO: use dev module (this is just the mage port)

const (
	typescriptRuntimeSubdir    = "sdk/typescript/runtime"
	typescriptGeneratedAPIPath = "sdk/typescript/api/client.gen.ts"

	nodeVersionMaintenance = "18"
	nodeVersionLTS         = "20"

	bunVersion = "1.1.12"
)

type TypescriptSDK struct {
	Dagger *DaggerDev // +private
}

// Lint the Typescript SDK
func (t TypescriptSDK) Lint(ctx context.Context) (rerr error) {
	eg, ctx := errgroup.WithContext(ctx)

	base := t.nodeJsBase()

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint the Typescript SDK code")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		_, err := base.WithExec([]string{"yarn", "lint"}).Sync(ctx)
		return err
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint Typescript snippets in the docs")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		path := "docs/current_docs"
		_, err := base.
			WithDirectory(
				fmt.Sprintf("/%s", path),
				t.Dagger.Source().Directory(path),
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

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that the generated client library is up-to-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := t.Dagger.Source()
		after, err := t.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.
			Dirdiff().
			AssertEqual(ctx, before, after, []string{typescriptGeneratedAPIPath})
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint the typescript runtime, which is written in Go")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		return dag.
			Go(t.Dagger.WithModCodegen().Source()).
			Lint(ctx, dagger.GoLintOpts{Packages: []string{typescriptRuntimeSubdir}})
	})

	return eg.Wait()
}

// Test the Typescript SDK
func (t TypescriptSDK) Test(ctx context.Context) (rerr error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return err
	}

	eg, ctx := errgroup.WithContext(ctx)

	// Loop over the LTS and Maintenance versions and test them
	for _, version := range []string{nodeVersionLTS, nodeVersionMaintenance} {
		base := t.nodeJsBaseFromVersion(version).With(installer)

		eg.Go(func() error {
			_, err := base.
				WithExec([]string{"yarn", "test:node", "-i", "-g", "Automatic Provisioned CLI Binary"}).
				Sync(ctx)
			return err
		})
	}

	eg.Go(func() error {
		_, err = t.bunJsBase().
			With(installer).
			WithExec([]string{"bun", "test:bun", "-i", "-g", "Automatic Provisioned CLI Binary"}).
			Sync(ctx)
		return err
	})

	return eg.Wait()
}

// Regenerate the Typescript SDK API
func (t TypescriptSDK) Generate(ctx context.Context) *dagger.Directory {
	generated := t.nodeJsBase().
		WithDirectory(
			"sdk/typescript/api",
			dag.Codegen().Codegen("typescript", dag.Engine().AsCodegenSidecar()),
		).
		WithExec([]string{"yarn", "fmt", typescriptGeneratedAPIPath}).
		File("sdk/typescript/api/client.gen.ts")
	return dag.Directory().WithFile("sdk/typescript/api/client.gen.ts", generated)
}

// Test the publishing process
func (t TypescriptSDK) TestPublish(ctx context.Context, tag string) error {
	return t.Publish(ctx, tag, true, nil, "https://github.com/dagger/dagger.git", nil)
}

// Publish the Typescript SDK
func (t TypescriptSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,
	// +optional
	npmToken *dagger.Secret,

	// +optional
	// +default="https://github.com/dagger/dagger.git"
	gitRepoSource string,
	// +optional
	githubToken *dagger.Secret,
) error {
	version, isVersioned := strings.CutPrefix(tag, "sdk/typescript/")
	versionFlag := strings.TrimPrefix(version, "v")
	if dryRun {
		versionFlag = "prepatch"
	}

	build := t.nodeJsBase().
		WithExec([]string{"npm", "run", "build"}).
		WithExec([]string{"npm", "version", versionFlag})
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
	if err != nil {
		return err
	}

	if isVersioned {
		if err := githubRelease(ctx, githubReleaseOpts{
			tag:         tag,
			notes:       sdkChangeNotes(t.Dagger.Src, "typescript", version),
			gitRepo:     gitRepoSource,
			githubToken: githubToken,
			dryRun:      dryRun,
		}); err != nil {
			return err
		}
	}

	return nil
}

// Bump the Typescript SDK's Engine dependency
func (t TypescriptSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	engineReference := fmt.Sprintf("// Code generated by dagger. DO NOT EDIT.\n"+
		"export const CLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return dag.Directory().WithNewFile("sdk/typescript/provisioning/default.ts", engineReference), nil
}

func (t TypescriptSDK) nodeJsBase() *dagger.Container {
	// Use the LTS version by default
	return t.nodeJsBaseFromVersion(nodeVersionMaintenance)
}

func (t TypescriptSDK) nodeJsBaseFromVersion(nodeVersion string) *dagger.Container {
	appDir := "sdk/typescript"
	src := t.Dagger.Source().Directory(appDir)

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

func (t TypescriptSDK) bunJsBase() *dagger.Container {
	appDir := "sdk/typescript"
	src := t.Dagger.Source().Directory(appDir)

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
