package main

import (
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
	"typescript-sdk/tsutils"
)

type LibGenerator struct {
	//+private
	Ctr *dagger.Container

	//+private
	StaticBundleLib *dagger.Directory

	//+private
	StaticLocalLib *dagger.Directory
}

func NewLibGenerator(sdkSourceDir *dagger.Directory) *LibGenerator {
	ctr := dag.
		Container().
		From(tsdistconsts.DefaultBunImageRef).
		WithMountedFile(codegenBinPath, sdkSourceDir.File("/codegen"))

	return &LibGenerator{
		Ctr:             ctr,
		StaticBundleLib: sdkSourceDir.Directory("/bundled_lib"),
		StaticLocalLib: sdkSourceDir.
			WithoutDirectory("codegen").
			WithoutDirectory("runtime").
			WithoutDirectory("tsx_module").
			WithoutDirectory("bundled_lib").
			WithoutDirectory("src/provisioning").
			WithoutFile("src/module/entrypoint/introspection_entrypoint.ts"),
	}
}

// GenerateBindings generates the client bindings for the given module.
func (l *LibGenerator) GenerateBindings(
	introspectionJSON *dagger.File,
	moduleName string,
	modulePath string,
	libOrigin SDKLibOrigin,
) *dagger.File {
	codegenArgs := []string{
		codegenBinPath,
		"generate-module",
		"--lang", "typescript",
		"--output", ModSourceDirPath,
		"--module-name", moduleName,
		"--module-source-path", modulePath,
		"--introspection-json-path", schemaPath,
	}

	if libOrigin == Bundle {
		codegenArgs = append(codegenArgs, "--bundle")
	}

	return l.Ctr.
		WithWorkdir(modulePath).
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory(modulePath).
		File("sdk/src/api/client.gen.ts")
}

// Add the bundle library (code.js & core.d.ts) to the sdk directory.
// Add the static export setup (index.ts & client.gen.ts) to the sdk directory.
// Generate the client.gen.ts file using the introspection file.
func (l *LibGenerator) GenerateBundleLibrary(
	introspectionJSON *dagger.File,
	moduleName string,
	modulePath string,
) *dagger.Directory {
	return l.StaticBundleLib.
		WithNewFile("index.ts", tsutils.StaticBundleIndexTS).
		WithNewFile("telemetry.ts", tsutils.StaticBundleTelemetryTS).
		WithFile(
			"client.gen.ts",
			l.GenerateBindings(
				introspectionJSON,
				moduleName,
				modulePath,
				Bundle,
			),
		)
}

// Copy the complete Typescript SDK directory
// Generate the client.gen.ts file using the introspection file.
// TODO(TomChv): We should deprecate local lib support in the future.
func (l *LibGenerator) GenerateLocalLibrary(
	introspectionJSON *dagger.File,
	moduleName string,
	modulePath string,
) *dagger.Directory {
	return l.StaticLocalLib.
		WithFile(
			"src/api/client.gen.ts",
			l.GenerateBindings(
				introspectionJSON,
				moduleName,
				modulePath,
				Local,
			),
		)
}
