// Runtime module for the PHP SDK

package main

import (
	"context"
)

const (
	DefaultImage          = "php:8.3-cli-alpine"
	// TODO: figure out issue with Directory.Diff() "/" != "/src"
	ModSourceDirPath      = "/"
	RuntimeExecutablePath = "/"
	GenDir                = "sdk"
	GenPath               = "../generated"
	SchemaPath            = "/schema.json"
	LockFilePath          = "requirements.lock"
)

type PhpSdk struct {
	SourceDir  *Directory
	RequiredPaths []string
	Container *Container
}

func New(
	// Directory with the PHP SDK source code.
	// +optional
	sourceDir *Directory,
) *PhpSdk {
	if sourceDir == nil {
		sourceDir = dag.
			Directory().
			WithNewFile("index.php", "<?php\n echo 'hi';")
	}
	
	return &PhpSdk{
		SourceDir:  sourceDir,

		RequiredPaths: []string{},

		/**
		 *  dag is a *Client Object
		 * https://pkg.go.dev/dagger.io/dagger@v0.11.4#Client
		 * dag.Container() creates a "scratch container" (Container Object)
		 * https://pkg.go.dev/dagger.io/dagger@v0.11.4#Client.Container
		 * https://pkg.go.dev/dagger.io/dagger@v0.11.4#Container
		 * Container.From() initialises the Container from a pulled base image
		 */
		Container: dag.Container().From(DefaultImage),
	}
}

func (sdk *PhpSdk) Codegen() (*GeneratedCode, error) {

	/**
	 * returns the container with a directory mounted at the given path
	 * https://pkg.go.dev/dagger.io/dagger@v0.11.4#Container.WithMountedDirectory
	 */ 
	ctr := sdk.Container.WithMountedDirectory(ModSourceDirPath, sdk.SourceDir)
	//		WithMountedDirectory(RuntimeExecutablePath, dag.CurrentModule().Source()).
	//	WithMountedDirectory(GenDir, dag.CurrentModule().Source().Directory(GenPath))
	

	// ctr = ctr.WithMountedDirectory("/codegen", sdk.SourceDir.Directory("src")).
	//	WithMountedFile("/")

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
			WithVCSGeneratedPaths([]string{GenDir + "/**"}).
			WithVCSIgnoredPaths([]string{GenDir}),
		nil
}

// Container for executing the PHP module runtime
func (sdk *PhpSdk) ModuleRuntime(
	ctx context.Context,
	modSource *ModuleSource,
	introspectionJSON string,
) (*Container, error) {
	ctr := sdk.Container

	return ctr.WithEntrypoint([]string{RuntimeExecutablePath}), nil
}



// // Returns a container that echoes whatever string argument is provided
// func (m *PhpSdk) ContainerEcho(stringArg string) *Container {
//	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
// }
//
// // Returns lines that match a pattern in the files of the provided Directory
// func (m *PhpSdk) GrepDir(ctx context.Context, directoryArg *Directory, pattern string) (string, error) {
//	return dag.Container().
//		From("alpine:latest").
//		WithMountedDirectory("/mnt", directoryArg).
//		WithWorkdir("/mnt").
//		WithExec([]string{"grep", "-R", pattern, "."}).
//		Stdout(ctx)
// }
