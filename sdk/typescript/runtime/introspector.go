package main

import (
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
)

const (
	introspectorBinPath = "/bin/ts-introspector"
	typescriptLibPath   = "/src/node_modules/typescript"
)

type Introspector struct {
	//+private
	Ctr *dagger.Container
}

func NewIntrospector(sdkSourceDir *dagger.Directory) *Introspector {
	ctr := dag.
		Container().
		From(tsdistconsts.DefaultBunImageRef).
		WithMountedFile(introspectorBinPath, sdkSourceDir.File(introspectorBinPath)).
		WithMountedDirectory(typescriptLibPath, sdkSourceDir.Directory("typescript-library")).
		WithWorkdir(ModSourceDirPath)

	return &Introspector{
		Ctr: ctr,
	}
}

func (i *Introspector) AsEntrypoint(
	outputFilePath string,

	moduleName string,

	sourceCode *dagger.Directory,

	clientBindings *dagger.File,
) *dagger.Container {
	return i.Ctr.
		WithMountedDirectory("src", sourceCode).
		WithMountedFile("sdk/client.gen.ts", clientBindings).
		WithEnvVariable("TYPEDEF_OUTPUT_FILE", outputFilePath).
		WithEntrypoint([]string{introspectorBinPath, moduleName, "src", "sdk/client.gen.ts"})
}
