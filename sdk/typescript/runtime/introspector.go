package main

import (
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
	"typescript-sdk/tsutils"
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
	// Synthesize a minimal @dagger.io/dagger package so TS can resolve the bare import.
	sdkPkg := dag.Directory().
		WithFile("client.gen.ts", clientBindings).
		WithNewFile("index.ts", tsutils.StaticBundleModuleIndexTS).
		WithNewFile("core.d.ts", tsutils.StaticBundleCoreDTS).
		WithNewFile("telemetry.ts", tsutils.StaticBundleTelemetryTS)

	return i.Ctr.
		WithMountedDirectory("src", sourceCode).
		// Make it resolvable by Node/TS: @dagger.io/dagger -> node_modules package
		WithMountedDirectory("node_modules/@dagger.io/dagger", sdkPkg).
		// Keep the old location too so the CLI arg still points to a file
		WithMountedDirectory("sdk", sdkPkg).
		WithEnvVariable("TYPEDEF_OUTPUT_FILE", outputFilePath).
		WithEntrypoint([]string{introspectorBinPath, moduleName, "src", "sdk/client.gen.ts"})
}
