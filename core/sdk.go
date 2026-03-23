package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
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

		// Introspection JSON file for the schema visible to the client.
		dagql.Result[*File],

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
ModuleRuntime is an abstraction over different ways to execute module code.

This can be either:
- A Container-based runtime (traditional SDKs)
- A native execution environment (e.g., Dang running directly in the engine)
*/
type ModuleRuntime interface {
	// AsContainer returns the runtime as a Container, if applicable.
	// Returns false if this runtime doesn't use containers.
	AsContainer() (dagql.ObjectResult[*Container], bool)

	// Call executes a function call in this runtime.
	// The runtime is responsible for preparing the execution environment,
	// running the function, and returning the result.
	// Returns the output bytes and the client ID that was used for execution.
	Call(
		ctx context.Context,
		execMD *buildkit.ExecutionMetadata,
		fnCall *FunctionCall,
	) (outputBytes []byte, clientID string, err error)
}

/*
ContainerRuntime wraps a Container to serve as a ModuleRuntime.
*/
type ContainerRuntime struct {
	Container dagql.ObjectResult[*Container]
}

func (r *ContainerRuntime) AsContainer() (dagql.ObjectResult[*Container], bool) {
	return r.Container, true
}

func (r *ContainerRuntime) Call(
	ctx context.Context,
	execMD *buildkit.ExecutionMetadata,
	fnCall *FunctionCall,
) ([]byte, string, error) {
	srv := dagql.CurrentDagqlServer(ctx)

	// Use the inner (canonical) server for core API plumbing so that
	// entrypoint proxies on the outer server cannot shadow core fields
	// like "directory" and cause infinite recursion.
	coreSrv := srv
	if srv.Inner != nil {
		coreSrv = srv.Inner
	}

	var metaDir dagql.ObjectResult[*Directory]
	err := coreSrv.Select(ctx, coreSrv.Root(), &metaDir,
		dagql.Selector{
			Field: "directory",
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("create mod metadata directory: %w", err)
	}

	var ctr dagql.ObjectResult[*Container]
	err = srv.Select(ctx, r.Container, &ctr,
		dagql.Selector{
			Field: "withMountedDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(modMetaDirPath)},
				{Name: "source", Value: dagql.NewID[*Directory](metaDir.ID())},
			},
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("exec function: %w", err)
	}

	execCtx := ctx
	execCtx = dagql.WithSkip(execCtx) // this span shouldn't be shown (it's entirely useless)
	err = srv.Select(execCtx, ctr, &ctr,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{Name: "args", Value: dagql.ArrayInput[dagql.String]{}},
				{Name: "useEntrypoint", Value: dagql.NewBoolean(true)},
				{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(true)},
				{Name: "execMD", Value: dagql.NewSerializedString(execMD)},
			},
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("exec function: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get buildkit client: %w", err)
	}

	_, err = ctr.Self().Evaluate(ctx)
	if err != nil {
		return nil, "", r.handleCallError(ctx, fnCall, bk, err)
	}

	ctrOutputDir, err := ctr.Self().Directory(ctx, modMetaDirPath)
	if err != nil {
		return nil, "", fmt.Errorf("get function output directory: %w", err)
	}

	modMetaFile, err := ctrOutputDir.File(ctx, modMetaOutputPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get mod meta file: %w", err)
	}

	// Read the output of the function
	outputBytes, err := modMetaFile.Contents(ctx, nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("read function output file: %w", err)
	}

	// Get the client ID actually used during the function call - this might not
	// be the same as execMD.ClientID if the function call was cached at the
	// buildkit level
	clientID, err := ctr.Self().usedClientID(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("get client ID from container: %w", err)
	}

	return outputBytes, clientID, nil
}

func (r *ContainerRuntime) handleCallError(ctx context.Context, call *FunctionCall, bk *buildkit.Client, baseErr error) error {
	id, ok, extractErr := extractError(ctx, bk, baseErr)
	if extractErr != nil {
		// if the module hasn't provided us with a nice error, just return the
		// original error
		return baseErr
	}
	if ok {
		srv := dagql.CurrentDagqlServer(ctx)
		errInst, err := id.Load(ctx, srv)
		if err != nil {
			return fmt.Errorf("load error instance: %w", err)
		}
		dagErr := errInst.Self().Clone()
		originCtx := trace.SpanContextFromContext(
			telemetry.Propagator.Extract(
				context.Background(),
				telemetry.AnyMapCarrier(dagErr.Extensions()),
			),
		)
		if !originCtx.IsValid() {
			// If the Error doesn't already have an origin, inject the current trace
			// context as its origin.
			tm := propagation.MapCarrier{}
			telemetry.Propagator.Inject(ctx, tm)
			for _, key := range tm.Keys() {
				val := tm.Get(key)
				valJSON, err := json.Marshal(val)
				if err != nil {
					return fmt.Errorf("marshal value: %w", err)
				}
				dagErr.Values = append(dagErr.Values, &ErrorValue{
					Name:  key,
					Value: JSON(valJSON),
				})
			}
		}
		return dagErr
	}
	if call.Name == "" {
		return fmt.Errorf("call constructor: %w", baseErr)
	}
	return fmt.Errorf("call function %q: %w", call.Name, baseErr)
}

/*
Runtime is an interface that a SDK may implements to provide an executable
environment to run the module's code at runtime.

This include setup of the runtime environment, dependencies installation,
and entrypoint setup.

For container-based runtimes, the returned ModuleRuntime should wrap a container
with an entrypoint that will register the module typedefs in the Dagger
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
		Runtime returns an execution environment that is used to execute module code
		at runtime in the Dagger engine.

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
	) (ModuleRuntime, error)
}

/*
ModuleTypes is an interface that a SDK may implement to expose type definitions of the module.

This interface MUST be implemented to support self calls.
*/
type ModuleTypes interface {
	/*
		ModuleTypes returns a module instance representing the type definitions
		exposed by the module code.

		This function prototype is different from the one exposed by the SDK.
		SDK must implement the `ModuleTypes` function with the following signature:

		```gql
		  moduleTypes(
		    modSource: ModuleSource!
		    introspectionJSON: File!
			outputFilePath: String!
		  ): Container!
		```
	*/
	ModuleTypes(
		context.Context,

		// Current module dependencies.
		*ModDeps,

		// Current instance of the module source.
		dagql.ObjectResult[*ModuleSource],

		// Call ID to perform the call against the right module
		*call.ID,
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

	// Transform the SDK into a ModuleTypes if it implements it.
	AsModuleTypes() (ModuleTypes, bool)

	// Transform the SDK into a CodeGenerator if it implements it.
	AsCodeGenerator() (CodeGenerator, bool)

	// Transform the SDK into a ClientGenerator if it implements it.
	AsClientGenerator() (ClientGenerator, bool)
}
