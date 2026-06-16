package main

import (
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"
)

func defaultPackageJSONFile() *dagger.File {
	return dag.File("package.json", tsutils.StaticDefaultPackageJSON)
}
