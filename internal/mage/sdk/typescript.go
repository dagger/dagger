package sdk

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/internal/mage/util"
)

var typescriptGeneratedAPIPath = "sdk/typescript/api/client.gen.ts"

var _ SDK = TypeScript{}

type TypeScript mg.Namespace

type NodeVersion string

const (
	Maintenance NodeVersion = "18"
	LTS         NodeVersion = "20"
)

// Lint lints the TypeScript SDK
func (t TypeScript) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("typescript").Pipeline("lint")

	eg, gctx := errgroup.WithContext(ctx)

	base := nodeJsBase(c)

	eg.Go(func() error {
		_, err = base.WithExec([]string{"yarn", "lint"}).Sync(gctx)
		return err
	})

	eg.Go(func() error {
		path := "docs/current_docs"
		_, err = base.
			WithDirectory(
				fmt.Sprintf("/%s", path),
				util.Repository(c).Directory(path),
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
			Sync(gctx)
		return err
	})

	eg.Go(func() error {
		return util.LintGeneratedCode("sdk:typescript:generate", func() error {
			return t.Generate(gctx)
		}, typescriptGeneratedAPIPath)
	})

	return eg.Wait()
}

// Test tests the TypeScript SDK
func (t TypeScript) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("typescript").Pipeline("test")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-typescript-test"})
	if err != nil {
		return err
	}

	cliBinary, err := util.DevelDaggerBinary(ctx, c)
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	eg, gctx := errgroup.WithContext(ctx)

	// Loop over the LTS and Maintenance versions and test them
	for _, version := range []NodeVersion{LTS, Maintenance} {
		version := version
		c := c.Pipeline(fmt.Sprintf("node:%s", string(version)))
		base := nodeJsBaseFromVersion(c, version)
		cliBinary := cliBinary

		eg.Go(func() error {
			_, err := base.
				WithServiceBinding("dagger-engine", devEngine).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
				WithMountedFile(cliBinPath, cliBinary).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
				WithExec([]string{"yarn", "test:node"}).
				Sync(gctx)
			return err
		})

	}

	eg.Go(func() error {
		cliBinary := cliBinary
		c := c.Pipeline("bun")
		_, err = bunJsBase(c).
			WithServiceBinding("dagger-engine", devEngine).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
			WithMountedFile(cliBinPath, cliBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithExec([]string{"bun", "test:bun"}).
			Sync(gctx)

		return err
	})

	return eg.Wait()
}

// Generate re-generates the SDK API
func (t TypeScript) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("typescript").Pipeline("generate")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-typescript-generate"})
	if err != nil {
		return err
	}

	cliBinary, err := util.DevelDaggerBinary(ctx, c)
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	generated, err := nodeJsBase(c).
		WithServiceBinding("dagger-engine", devEngine).
		WithMountedFile("/usr/local/bin/codegen", util.CodegenBinary(c)).
		WithMountedFile(cliBinPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"codegen", "--lang", "typescript", "-o", path.Dir(typescriptGeneratedAPIPath)}).
		WithExec([]string{
			"yarn",
			"fmt",
			typescriptGeneratedAPIPath,
		}).
		File(typescriptGeneratedAPIPath).
		Contents(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(typescriptGeneratedAPIPath, []byte(generated), 0o600)
}

// Publish publishes the TypeScript SDK
func (t TypeScript) Publish(ctx context.Context, tag string) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("typescript").Pipeline("publish")

	version := strings.TrimPrefix(tag, "sdk/typescript/v")

	dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN"))

	// build and set version
	build := nodeJsBase(c).
		WithExec([]string{"npm", "run", "build"}).
		WithExec([]string{"npm", "version", version})

	// configure .npmrc
	if !dryRun {
		token := util.GetHostEnv("NPM_TOKEN")
		npmrc := fmt.Sprintf(`//registry.npmjs.org/:_authToken=%s
registry=https://registry.npmjs.org/
always-auth=true`, token)
		build = build.WithMountedSecret(".npmrc", c.SetSecret("npmrc", npmrc))
	}

	// publish
	publish := build.WithExec([]string{"npm", "publish", "--access", "public"})
	if dryRun {
		publish = build.WithExec([]string{"npm", "publish", "--access", "public", "--dry-run"})
	}

	_, err = publish.Sync(ctx)
	return err
}

// Bump the TypeScript SDK's Engine dependency
func (t TypeScript) Bump(_ context.Context, version string) error {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	engineReference := fmt.Sprintf("// Code generated by dagger. DO NOT EDIT.\n"+
		"export const CLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return os.WriteFile("sdk/typescript/provisioning/default.ts", []byte(engineReference), 0o600)
}

func nodeJsBase(c *dagger.Client) *dagger.Container {
	// Use the LTS version by default
	return nodeJsBaseFromVersion(c, LTS)
}

func nodeJsBaseFromVersion(c *dagger.Client, nodeVersion NodeVersion) *dagger.Container {
	appDir := "sdk/typescript"
	src := c.Directory().WithDirectory("/", util.Repository(c).Directory(appDir))

	// Mirror the same dir structure from the repo because of the
	// relative paths in eslint (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	nodeVersionImage := fmt.Sprintf("node:%s-alpine", nodeVersion)

	return c.Container().
		// ⚠️  Keep this in sync with the engine version defined in package.json
		From(nodeVersionImage).
		WithWorkdir(mountPath).
		WithMountedCache("/usr/local/share/.cache/yarn", c.CacheVolume(fmt.Sprintf("yarn_cache:%s", nodeVersion))).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithFile(fmt.Sprintf("%s/yarn.lock", mountPath), src.File("yarn.lock")).
		WithExec([]string{"yarn", "install"}).
		WithDirectory(mountPath, src)
}

func bunJsBase(c *dagger.Client) *dagger.Container {
	appDir := "sdk/typescript"
	src := c.Directory().WithDirectory("/", util.Repository(c).Directory(appDir))

	// Mirror the same dir structure from the repo because of the
	// relative paths in eslint (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	return c.Container().
		From("oven/bun:1.0.27").
		WithWorkdir(mountPath).
		WithMountedCache("/root/.bun/install/cache", c.CacheVolume("bun_cache")).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithExec([]string{"bun", "install"}).
		WithDirectory(mountPath, src)
}
