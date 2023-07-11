package main

import (
	"fmt"
	"strings"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

const nodejsAppDir = "sdk/nodejs"

type NodejsTargets struct {
	Targets
}

func (t NodejsTargets) sdkSrcDir(ctx dagger.Context) *dagger.Directory {
	return t.srcDir(ctx).Directory(nodejsAppDir)
}

func (t NodejsTargets) baseImage(ctx dagger.Context) *dagger.Container {
	src := t.sdkSrcDir(ctx)

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
func (t NodejsTargets) NodejsLint(ctx dagger.Context) (string, error) {
	eg, gctx := errgroup.WithContext(ctx)

	var yarnLintOut string
	eg.Go(func() error {
		var err error
		yarnLintOut, err = t.baseImage(ctx).
			WithExec([]string{"yarn", "lint"}).
			Stderr(gctx)
		return err
	})

	var docLintOut string
	eg.Go(func() error {
		path := "docs/current"
		var err error
		docLintOut, err = t.baseImage(ctx).
			WithDirectory(
				fmt.Sprintf("/%s", path),
				t.srcDir(ctx).Directory(path),
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
