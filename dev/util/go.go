package util

import (
	"github.com/dagger/dagger/ci/internal/dagger"
)

var dag = dagger.Connect()

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
