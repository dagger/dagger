package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/build"
	"github.com/dagger/dagger/ci/internal/dagger"
)

// TODO: use dev module (this is just the mage port)

const (
	typescriptGeneratedAPIPath = "sdk/typescript/api/client.gen.ts"

	nodeVersionMaintenance = "18"
	nodeVersionLTS         = "20"

	bunVersion = "1.1.12"
)

type TypescriptSDK struct {
	Dagger *Dagger // +private
}

// Lint the Typescript SDK, and return an error in case of issue
func (t TypescriptSDK) Lint(ctx context.Context) error {
	report, err := t.LintReport(ctx)
	if err != nil {
		return err
	}
	return report.AssertPass(ctx)
}

func filterDirectory(input *Directory, include []string) *Directory {
	return dag.
		Directory().
		WithDirectory("/", input, dagger.DirectoryWithDirectoryOpts{Include: include})
}

// Keep only typescript-related files from a directory
func onlyTypescript(input *Directory) *Directory {
	return filterDirectory(input, []string{
		"**/*.mts",
		"**/*.mjs",
		"**/*.ts",
		"**/*.js",
		"*prettier*",
		"*eslint*",
		"**/package.json",
	})
}

// Produce a lint report for the Typescript SDK
// FIXME: rename this to Lint soon, it's a better interface
func (t TypescriptSDK) LintReport(ctx context.Context) (*LintReport, error) {
	return t.lintReport(
		ctx,
		// runtime source
		t.Dagger.Source.Directory("sdk/typescript/runtime"),
		// client source
		onlyTypescript(t.Dagger.Source.Directory("sdk/typescript")),
		// docs
		onlyTypescript(t.Dagger.Source.Directory("docs/current_docs")),
	)
}

func (t TypescriptSDK) Docs() *Directory {
	return onlyTypescript(t.Dagger.Source.Directory("docs/current_docs"))
}

func (t TypescriptSDK) lintReport(
	ctx context.Context,
	// Source code of the Typescript runtime (written in Go)
	// +default="/sdk/typescript/runtime"
	runtimeSource *Directory,
	// Source code of the Typescript client and associated tooling
	// +default="/sdk/typescript"
	// +ignore=["*", "!**/*.mts", "!**/*.mjs", "!**/*.ts", "!**/*.js", "!*prettier", "!*eslint", "!package.json"]
	clientSource *Directory,
	// Documentation source (which contains typescript snippets)
	// +default="/docs/current_docs"
	// +ignore=["*", "!**/*.mts", "!**/*.mjs", "!**/*.ts", "!**/*.js", "!*prettier", "!*eslint", "!package.json"]
	docs *Directory,
) (*LintReport, error) {
	report := new(LintReport)
	eg, ctx := errgroup.WithContext(ctx)
	// Lint the client source code
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "Lint the typescript client library and associated tooling")
		defer span.End()
		clientReport, err := new(TypescriptLint).Lint(ctx, clientSource)
		if err != nil {
			return err
		}
		return report.merge(clientReport)
	})
	// Lint the docs
	//eg.Go(func() error {
	//	ctx, span := Tracer().Start(ctx, "Lint typescript snippets in the docs")
	//	defer span.End()
	//	docsReport, err := new(TypescriptLint).
	//		// FIXME: why not use eslint.config.js in the docs directory?
	//		WithConfig(clientSource.File("eslint-docs.config.js")).
	//		Lint(ctx, docs)
	//	if err != nil {
	//		return err
	//	}
	//	return report.merge(docsReport)
	//})
	// Check that generated client library is up-to-date
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "Check that generated client library is up-to-date")
		defer span.End()
		codegenReport, err := t.CheckGenerated(ctx)
		if err != nil {
			return err
		}
		return report.merge(codegenReport)
	})
	//Lint the runtime
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "Lint the Typescript runtime (which is written in Go)")
		defer span.End()
		runtimeReport, err := new(GoLint).Lint(ctx, runtimeSource)
		if err != nil {
			return err
		}
		return report.merge(runtimeReport)
	})
	return report, eg.Wait()
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
		File(typescriptGeneratedAPIPath)
	return dag.Directory().WithFile(typescriptGeneratedAPIPath, generated), nil
}

func (t TypescriptSDK) CheckGenerated(ctx context.Context) (*LintReport, error) {
	before := filterDirectory(t.Dagger.Source, []string{typescriptGeneratedAPIPath})
	after, err := t.Generate(ctx)
	if err != nil {
		return nil, err
	}
	diff, err := dag.Dirdiff().DiffRaw(ctx, before, after)
	if err != nil {
		return nil, err
	}
	report := new(LintReport)
	if len(diff) > 0 {
		report.Issues = append(report.Issues, LintIssue{
			Text:    typescriptGeneratedAPIPath + ": generated typescript client is not up-to-date",
			IsError: true,
			Tool:    "TypescriptSDK.checkGenerated",
		})
	}
	return report, nil
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
	if dryRun {
		version = "prepatch"
	}

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
func (t TypescriptSDK) Bump(ctx context.Context, version string) (*Directory, error) {
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
		From("oven/bun:"+bunVersion+"-alpine").
		WithWorkdir(mountPath).
		WithMountedCache("/root/.bun/install/cache", dag.CacheVolume("bun_cache")).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithExec([]string{"bun", "install"}).
		WithDirectory(mountPath, src)
}
