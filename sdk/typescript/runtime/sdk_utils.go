package main

import (
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"
)

func entrypointFile() *dagger.File {
	return dag.File("__dagger.entrypoint.ts", tsutils.StaticEntrypointTS)
}

func defaultPackageJSONFile() *dagger.File {
	return dag.File("package.json", tsutils.StaticDefaultPackageJSON)
}
