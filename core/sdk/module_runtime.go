package sdk

import (
	"context"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// A SDK module that implements the `Runtime` interface
type runtimeModule struct {
	mod *module
}

func (sdk *runtimeModule) HasModuleTypeDefs() bool {
	_, ok := sdk.mod.funcs["moduleTypeDefs"]
	return ok
}

func (sdk *runtimeModule) TypeDefs(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	if !sdk.HasModuleTypeDefs() {
		return inst, fmt.Errorf("failed to get typedefs object: module %s does not implement moduleTypeDefs", sdk.mod.mod.Self().Name())
	}

	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load typedefs object")
	defer telemetry.End(span, func() error { return rerr })

	dag, err := sdk.mod.dag(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx, []string{"Host"})
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	rerr = dag.Select(ctx, sdk.mod.sdk, &inst,
		dagql.Selector{
			Field: "moduleTypeDefs",
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
		})
	return
}

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
) (inst dagql.ObjectResult[*core.Container], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.End(span, func() error { return rerr })

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx, []string{"Host"})
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	dag, err := sdk.mod.dag(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

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
		return inst, fmt.Errorf("failed to call sdk moduleRuntime: %w", err)
	}
	return inst, nil
}
