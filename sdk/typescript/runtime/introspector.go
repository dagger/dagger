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

	// clientBindings is the directory of generated bindings: client.gen.ts plus
	// any per-dependency <dep>.gen.ts files. The whole directory is mounted so
	// that types re-exported from client.gen.ts (e.g. dep types) resolve.
	clientBindings *dagger.Directory,
) *dagger.Container {
	// Synthesize a minimal @dagger.io/dagger package so TS can resolve the bare import.
	sdkPkg := dag.Directory().
		WithDirectory(".", clientBindings).
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

// EmitEntrypoint runs the introspector binary in typedef-JSON mode and then
// invokes the Go-side `cmd/codegen generate-entrypoint` subcommand to render
// the static dispatch `__dagger.entrypoint.ts` file. Returns that file.
//
// The split lets the emitter logic live in Go (alongside the rest of
// cmd/codegen) while keeping the TypeScript-specific introspection in TS.
func (i *Introspector) EmitEntrypoint(
	moduleName string,

	sourceCode *dagger.Directory,

	// clientBindings is the directory of generated bindings: client.gen.ts plus
	// any per-dependency <dep>.gen.ts files.
	clientBindings *dagger.Directory,

	sdkSourceDir *dagger.Directory,
) *dagger.File {
	const typedefPath = "/work/typedef.json"

	sdkPkg := dag.Directory().
		WithDirectory(".", clientBindings).
		WithNewFile("index.ts", tsutils.StaticBundleModuleIndexTS).
		WithNewFile("core.d.ts", tsutils.StaticBundleCoreDTS).
		WithNewFile("telemetry.ts", tsutils.StaticBundleTelemetryTS)

	// Step 1: run the introspector to write the typedef JSON.
	withTypedef := i.Ctr.
		WithWorkdir("/work").
		WithMountedDirectory("/work/src", sourceCode).
		WithMountedDirectory("/work/node_modules/@dagger.io/dagger", sdkPkg).
		WithMountedDirectory("/work/sdk", sdkPkg).
		WithEnvVariable("EMIT_TYPEDEF_JSON_FILE", typedefPath).
		WithExec([]string{introspectorBinPath, moduleName, "src", "sdk/client.gen.ts"})

	// Step 2: hand the typedef JSON to `cmd/codegen generate-entrypoint`.
	const entrypointFile = "__dagger.entrypoint.ts"
	return dag.Container().
		From(tsdistconsts.DefaultBunImageRef).
		WithMountedFile(codegenBinPath, sdkSourceDir.File("/codegen")).
		WithMountedFile(typedefPath, withTypedef.File(typedefPath)).
		WithWorkdir("/work").
		WithExec([]string{
			codegenBinPath,
			"--lang", "typescript",
			"--output", "/work",
			"generate-entrypoint",
			"--typedef-json-path", typedefPath,
			"--output-file", entrypointFile,
			"--module-root", "/work",
			"--sdk-import", "@dagger.io/dagger",
			"--source-dir", "src",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		File("/work/" + entrypointFile)
}
