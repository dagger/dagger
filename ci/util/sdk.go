package main

import (
	"fmt"
)

func (repo *Repository) pythonSDK() *Directory {
	return dag.Directory().WithDirectory("/", repo.Directory, DirectoryWithDirectoryOpts{
		Include: []string{
			"pyproject.toml",
			"src/**/*.py",
			"src/**/*.typed",
			"runtime/",
			"LICENSE",
			"README.md",
			"dagger.json",
		},
	})
}

func (repo *Repository) typescriptSDK(arch string) *Directory {
	return dag.Directory().WithDirectory("/", repo.Directory, DirectoryWithDirectoryOpts{
		Include: []string{
			"**/*.ts",
			"LICENSE",
			"README.md",
			"runtime",
			"package.json",
			"dagger.json",
		},
		Exclude: []string{
			"node_modules",
			"dist",
			"**/test",
			"**/*.spec.ts",
			"dev",
		},
	}).WithFile("/codegen", repo.codegenBinary(arch, ""))
}

func (repo *Repository) goSDKImageTarBall(arch string) *File {
	return dag.Container(ContainerOpts{Platform: Platform("linux/" + arch)}).
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		WithEnvVariable("GOTOOLCHAIN", "auto").
		WithFile("/usr/local/bin/codegen", repo.codegenBinary(arch, "")).
		WithEntrypoint([]string{"/usr/local/bin/codegen"}).
		AsTarball()
}
