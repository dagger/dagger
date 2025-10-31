package main

import (
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"
)

func templateDirectory() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("template")
}

func binDirectory() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("bin")
}

func entrypointFile() *dagger.File {
	return dag.File("__dagger.entrypoint.ts", tsutils.StaticEntrypointTS)
}

func defaultPackageJSONFile() *dagger.File {
	return dag.File("package.json", tsutils.StaticDefaultPackageJSON)
}

func denoConfigUpdatorFile() *dagger.File {
	return binDirectory().File("__deno_config_updator.ts")
}

func tsConfigUpdatorFile() *dagger.File {
	return dag.CurrentModule().Source().File("bin/__tsconfig.updator.ts")
}

func bundledStaticDirectoryForModule() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("bundled_static_export/module")
}

func bundledStaticDirectoryForClientOnly() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("bundled_static_export/client")
}
