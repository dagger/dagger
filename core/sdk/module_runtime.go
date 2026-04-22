package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

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

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	dag, err := sdk.mod.dag(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

	var inst dagql.ObjectResult[*core.Container]
	err = dag.Select(ctx, sdk.mod.sdk, &inst,
		dagql.Selector{
			Field: "moduleRuntime",
			Args: []dagql.NamedInput{
				{
					Name:  "modSource",
					Value: dagql.NewID[*core.ModuleSource](source.ID()),
				},
				{
					Name:  "introspectionJson",
					Value: dagql.NewID[*core.File](schemaJSONFile.ID()),
				},
			},
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
