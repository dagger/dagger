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
	source dagql.Instance[*core.ModuleSource],
) (_ *core.Container, rerr error) {
	if !sdk.HasModuleTypeDefs() {
		return sdk.Runtime(ctx, deps, source)
	}

	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load typedefs")
	defer telemetry.End(span, func() error { return rerr })

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx, []string{"Host"})
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self.Name(), err)
	}

	return sdk.callModuleFn(ctx, "moduleTypeDefs", source, schemaJSONFile)
}

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.Instance[*core.ModuleSource],
) (_ *core.Container, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.End(span, func() error { return rerr })
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx, []string{"Host"})
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self.Name(), err)
	}

	return sdk.callModuleFn(ctx, "moduleRuntime", source, schemaJSONFile)
}

func (sdk *runtimeModule) callModuleFn(
	ctx context.Context,
	fnName string,
	source dagql.Instance[*core.ModuleSource],
	schemaJSONFile dagql.Instance[*core.File],
) (_ *core.Container, rerr error) {
	var inst dagql.Instance[*core.Container]
	err := sdk.mod.dag.Select(ctx, sdk.mod.sdk, &inst,
		dagql.Selector{
			Field: fnName,
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
	return inst.Self, nil
}
