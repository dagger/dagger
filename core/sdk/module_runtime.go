package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

// moduleRuntime's introspection argument, as registered in the SDK module's schema.
const introspectionJSONArgName = "introspectionJson"

// A SDK module that implements the `Runtime` interface
type runtimeModule struct {
	mod *module
}

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ core.ModuleRuntime, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.EndWithCause(span, &rerr)

	sdkInst, err := sdk.mod.instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	dag := sdkInst.dag

	source, err = scopeSourceForSDKOperation(ctx, source, "runtime", dag)
	if err != nil {
		return nil, fmt.Errorf("failed to scope module source for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	sourceID, err := source.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get scoped module source ID for sdk module %s runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	args := []dagql.NamedInput{
		{
			Name:  "modSource",
			Value: dagql.NewID[*core.ModuleSource](sourceID),
		},
	}

	if !sdk.skipRuntimeCodegen(source) {
		schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
		}
		schemaJSONFileID, err := schemaJSONFile.ID()
		if err != nil {
			return nil, fmt.Errorf("failed to get schema introspection json ID during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
		}
		args = append(args, dagql.NamedInput{
			Name:  introspectionJSONArgName,
			Value: dagql.NewID[*core.File](schemaJSONFileID),
		})
	}

	var inst dagql.ObjectResult[*core.Container]
	err = dag.Select(ctx, sdkInst.sdk, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args:  args,
		},
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(RuntimeWorkdirPath),
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk moduleRuntime: %w", err)
	}
	return &core.ContainerRuntime{Container: inst}, nil
}

// skipRuntimeCodegen reports whether the moduleRuntime call may omit the
// introspection JSON: the config format rules out runtime codegen and the
// SDK declared it builds from committed files (optional introspectionJson).
func (sdk *runtimeModule) skipRuntimeCodegen(src dagql.ObjectResult[*core.ModuleSource]) bool {
	return !useRuntimeCodegen(src) && sdk.mod.RuntimeTrustsCommittedFiles()
}
