package main

import (
	"fmt"
	"strings"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

const nodejsAppDir = "sdk/nodejs"

func nodejsSDKSrc(ctx dagger.Context) *dagger.Directory {
	return srcDir(ctx).Directory(nodejsAppDir)
}

func nodejsBase(ctx dagger.Context) *dagger.Container {
	src := nodejsSDKSrc(ctx)

	// Mirror the same dir structure from the repo because of the
	// relative paths in eslint (for docs linting).
	mountPath := fmt.Sprintf("/%s", nodejsAppDir)

	return ctx.Client().Container().
		// ⚠️  Keep this in sync with the engine version defined in package.json
		From("node:16-alpine").
		WithWorkdir(mountPath).
		WithMountedCache("/usr/local/share/.cache/yarn", ctx.Client().CacheVolume("yarn_cache")).
		WithFile(fmt.Sprintf("%s/package.json", mountPath), src.File("package.json")).
		WithFile(fmt.Sprintf("%s/yarn.lock", mountPath), src.File("yarn.lock")).
		WithExec([]string{"yarn", "install"}).
		WithDirectory(mountPath, src)
}

// Lint the Nodejs SDK
func NodejsLint(ctx dagger.Context) (string, error) {
	eg, gctx := errgroup.WithContext(ctx)

	var yarnLintOut string
	eg.Go(func() error {
		var err error
		yarnLintOut, err = nodejsBase(ctx).
			WithExec([]string{"yarn", "lint"}).
			Stderr(gctx)
		return err
	})

	var docLintOut string
	eg.Go(func() error {
		path := "docs/current"
		var err error
		docLintOut, err = nodejsBase(ctx).
			WithDirectory(
				fmt.Sprintf("/%s", path),
				srcDir(ctx).Directory(path),
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
			Stderr(gctx)
		return err
	})

	// TODO: test generated code too

	return strings.Join([]string{
		yarnLintOut,
		docLintOut,
	}, "\n"), eg.Wait()
}
