package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

/*
ClientGenerator is an interface that a module can implements to give
client generation capabilities.

The generated client is standalone and can be used in any project,
even if no source code module is available.

This interface MUST be implemented to support standalone client oriented
features (`dagger client install`).
*/
type ClientGenerator interface {
	/*
		RequiredClientGenerationFiles returns the list of files that are required
		from the host to generate the client.

		Note: this function is optional and may not be implemented by the SDK.
		It's only useful if the client generator needs files from the host while
		generating the client.

		This function prototype is different from the one exposed by the SDK.
		SDK MAY implement the `RequiredClientGenerationFiles` function with the
		following signature:

		```gql
		  requiredClientGenerationFiles: [String]!
		````
	*/
	RequiredClientGenerationFiles(ctx context.Context) (dagql.Array[dagql.String], error)

	/*
		Generate client binding for the module and its dependencies at the given
		output directory. The generated client will be placed in the same directory
		as the module source root dir and contains bindings for all of the module's
		dependencies in addition to the core API and the module itself if it got
		source code.

		It's up to that function to update the source root directory with additional
		configurations if needed.
		For example (executing go mod tidy, updating tsconfig.json etc...)

		The generated client should use the published library version of the SDK.
		However, depending on the local configuration, a copy of the current SDK
		library should be copied to test the generated client with latest changes.
		NOTE: this should only be used for testing purposes.
		For example (if go.mod has a replace directive on dagger.io/dagger for a local path)

		This function prototype is different from the one exposed by the SDK.
		SDK must implement the `GenerateClient` function with the following signature:

		```gql
		  generateClient(
			  modSource: ModuleSource!
			  introspectionJSON: File!
			  outputDir: String!
		  ): Directory!
		```
	*/
	GenerateClient(
		context.Context,

		// Current instance of the module source.
		dagql.ObjectResult[*ModuleSource],

		// Current module dependencies.
		*ModDeps,

		// Output directory of the generated client.
		string,
	) (dagql.ObjectResult[*Directory], error)
}

/*
CodeGenerator is an interface that a SDK may implements to generate code
for a module.

This can include multiple things, such as a generated client to interact
with the Dagger Engine but also any changes in the module's code to run
it in the Dagger Engine like dependency configuration, package manager
settings etc...

This interface MUST be implemented to support language oriented SDKs
(`dagger develop`).
*/
type CodeGenerator interface {
	/*
		Codegen generates code for the module at the given source directory and
		subpath.

		The Code field of the returned GeneratedCode object should be the generated
		contents of the module sourceDirSubpath, in the case where that's different
		than the root of the sourceDir.

		The provided Module is not fully initialized; the Runtime field will not be
		set yet.

		This function prototype is different from the one exposed by the SDK.
		SDK must implement the `Codegen` function with the following signature:

		```gql
		  codegen(
			  modSource: ModuleSource!
			  introspectionJSON: File!
		  ): GeneratedCode!
		```
	*/
	Codegen(
		context.Context,

		// Current module dependencies.
		*ModDeps,

		// Current instance of the module source.
		dagql.ObjectResult[*ModuleSource],
	) (*GeneratedCode, error)
}

/*
Runtime is an interface that a SDK may implements to provide an executable
container to run the module's code at runtime.

This include setup of the runtime environment, dependencies installation,
and entrypoint setup.

The returned container should have as entrypoint the execution of an
entrypoint function that will register the module typedefs in the Dagger
engine or execute a function of that module depending on the argument
forwarded to that entrypoint:
  - If the called object is empty, the script should register type definitions
    by sending a new ModuleID to the Dagger engine.
  - If the called object is set, the script should execute the corresponding
    function and send the result to the Dagger engine.

This interface MUST be implemented to support callable SDKs
(`dagger call`).
*/
type Runtime interface {
	/*
		Runtime returns a container that is used to execute module code at runtime
		in the Dagger engine.

		The provided Module is not fully initialized; the Runtime field will not
		be set yet.

		This function prototype is different from the one exposed by the SDK.
		SDK must implement the `Runtime` function with the following signature:

		```gql
		  moduleRuntime(
			  modSource: ModuleSource!
			  introspectionJSON: File!
		  ): Container!
		````
	*/
	Runtime(
		context.Context,

		// Current module dependencies.
		*ModDeps,

		// Current instance of the module source.
		dagql.ObjectResult[*ModuleSource],
	) (dagql.ObjectResult[*Container], error)

	/*
		HasModuleTypeDefs checks if the module exposes a `moduleTypeDefs` function
		to be called by `TypeDefs`.

		This doesn't rely on a function exposed by the SDK, but on the list of functions
		exposed.
	*/
	HasModuleTypeDefs() bool

	TypeDefs(
		context.Context,

		// Current module dependencies.
		*ModDeps,

		// Current instance of the module source.
		dagql.ObjectResult[*ModuleSource],
	) (dagql.ObjectResult[*Module], error)
}

/*
	  SDK aggregates all the interfaces that a SDK may implement.

		It provides conversion functions to get a specific interface if
		it's implemented.
		Otherwise, the function will return false.

		It works the same as type conversion in Go.
*/
type SDK interface {
	// Transform the SDK into a Runtime if it implements it.
	AsRuntime() (Runtime, bool)

	// Transform the SDK into a CodeGenerator if it implements it.
	AsCodeGenerator() (CodeGenerator, bool)

	// Transform the SDK into a ClientGenerator if it implements it.
	AsClientGenerator() (ClientGenerator, bool)
}
