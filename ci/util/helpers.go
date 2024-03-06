package util

import (
	"dagger/internal/dagger"
	"fmt"
)

func GoDirectory(dir *dagger.Directory) *dagger.Directory {
	return dag.Directory().WithDirectory("/", dir, dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			// go source
			"**/*.go",

			// modules
			"**/go.mod",
			"**/go.sum",

			// embedded files
			"**/*.tmpl",
			"**/*.ts.gtpl",
			"**/*.graphqls",
			"**/*.graphql",

			// misc
			".golangci.yml",
			"**/README.md", // needed for examples test
			"**/help.txt",  // needed for linting module bootstrap code
			"sdk/go/codegen/generator/typescript/templates/src/testdata/**/*",
			"core/integration/testdata/**/*",

			// Go SDK runtime codegen
			"**/dagger.json",
		},
		Exclude: []string{
			".git",
		},
	})
}

func GoBase(dir *dagger.Directory) *dagger.Container {
	dir = GoDirectory(dir)
	return dag.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		WithExec([]string{"apk", "add", "build-base"}).
		WithEnvVariable("CGO_ENABLED", "0").
		// adding the git CLI to inject vcs info
		// into the go binaries
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", dir, dagger.ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", dir).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build"))
}
