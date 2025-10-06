package main

import "typescript-sdk/internal/dagger"

func templateDirectory() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("template")
}

func binDirectory() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("bin")
}

func entrypointFile() *dagger.File {
	return binDirectory().File("__dagger.entrypoint.ts")
}

func denoConfigUpdatorFile() *dagger.File {
	return binDirectory().File("__deno_config_updator.ts")
}

func tsConfigUpdatorFile() *dagger.File {
	return binDirectory().File("__tsconfig.updator.ts")
}

func bundledStaticDirectoryForModule() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("bundled_static_export/module")
}

func bundledStaticDirectoryForClientOnly() *dagger.Directory {
	return dag.CurrentModule().Source().Directory("bundled_static_export/client")
}
