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

	Opts *LibGeneratorOpts
}

type LibGeneratorOpts struct {
	moduleName        string
	modulePath        string
	moduleSourceID    string
	genClient         bool
	coexistWithModule bool
}

func NewLibGenerator(sdkSourceDir *dagger.Directory, opts *LibGeneratorOpts) *LibGenerator {
	ctr := dag.
		Container().
		From(tsdistconsts.DefaultBunImageRef).
		WithMountedFile(codegenBinPath, sdkSourceDir.File("/codegen"))

	return &LibGenerator{
		Ctr:             ctr,
		Opts:            opts,
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
	libOrigin SDKLibOrigin,
	outputDir string,
) *dagger.File {
	ctr := l.Ctr

	codegenArgs := []string{codegenBinPath}
	if l.Opts.genClient {
		codegenArgs = append(codegenArgs, "generate-client")
	} else {
		codegenArgs = append(codegenArgs, "generate-module")
	}

	codegenArgs = append(codegenArgs,
		"--lang", "typescript",
		"--output", outputDir,
		"--introspection-json-path", schemaPath,
	)

	if l.Opts.moduleName != "" {
		codegenArgs = append(codegenArgs, "--module-name", l.Opts.moduleName)
	}

	if l.Opts.modulePath != "" {
		codegenArgs = append(codegenArgs, "--module-source-path", l.Opts.modulePath)
		ctr = ctr.WithWorkdir(l.Opts.modulePath)
	}

	if l.Opts.moduleSourceID != "" {
		codegenArgs = append(codegenArgs, "--module-source-id", l.Opts.moduleSourceID)
	}

	if libOrigin == Bundle && !l.Opts.genClient {
		codegenArgs = append(codegenArgs, "--bundle")
	}

	ctr = ctr.
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})

	if l.Opts.modulePath != "" {
		return ctr.
			Directory(l.Opts.modulePath).
			File("sdk/src/api/client.gen.ts")
	}

	return ctr.Directory(outputDir).File("client.gen.ts")
}

// Add the bundle library (code.js & core.d.ts) to the sdk directory.
// Add the static export setup (index.ts & client.gen.ts) to the sdk directory.
// Generate the client.gen.ts file using the introspection file.
func (l *LibGenerator) GenerateBundleLibrary(
	introspectionJSON *dagger.File,
	outputDir string,
) *dagger.Directory {
	result := l.StaticBundleLib.
		WithNewFile("telemetry.ts", tsutils.StaticBundleTelemetryTS).
		WithFile(
			"client.gen.ts",
			l.GenerateBindings(
				introspectionJSON,
				Bundle,
				outputDir,
			),
		)

	// If we're generating a standalone client, we export a lighter
	// version of the library.
	if l.Opts.genClient && !l.Opts.coexistWithModule {
		return result.
			WithNewFile("index.ts", tsutils.StaticBundleClientIndexTS)
	}

	return result.
		WithNewFile("index.ts", tsutils.StaticBundleModuleIndexTS)
}

// Copy the complete Typescript SDK directory
// Generate the client.gen.ts file using the introspection file.
// TODO(TomChv): We should deprecate local lib support in the future.
func (l *LibGenerator) GenerateLocalLibrary(
	introspectionJSON *dagger.File,
	outputDir string,
) *dagger.Directory {
	return l.StaticLocalLib.
		WithFile(
			"src/api/client.gen.ts",
			l.GenerateBindings(
				introspectionJSON,
				Local,
				outputDir,
			),
		)
}

func (l *LibGenerator) GenerateRemoteLibrary(
	introspectionJSON *dagger.File,
	outputDir string,
) *dagger.Directory {
	return dag.
		Directory().
		WithDirectory(outputDir,
			dag.Directory().
				WithFile("client.gen.ts", l.GenerateBindings(introspectionJSON, Remote, outputDir)),
		)
}
