package sdk

import (
	"context"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// A SDK module that implements the `CodeGenerator` interface
type codeGeneratorModule struct {
	mod *module
}

func (sdk *codeGeneratorModule) Codegen(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ *core.GeneratedCode, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: run codegen")
	defer telemetry.End(span, func() error { return rerr })

	dag, err := sdk.mod.dag(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema introspection json during %s module sdk codegen: %w", sdk.mod.mod.Self().Name(), err)
	}

	var inst dagql.Result[*core.GeneratedCode]
	err = dag.Select(ctx, sdk.mod.sdk, &inst, dagql.Selector{
		Field: "codegen",
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
	if err != nil {
		return nil, fmt.Errorf("failed to call sdk module codegen: %w", err)
	}
	return inst.Self(), nil
}
