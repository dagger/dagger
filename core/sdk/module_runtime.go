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

func (sdk *runtimeModule) Runtime(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.Instance[*core.ModuleSource],
) (inst dagql.Instance[*core.Container], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load runtime")
	defer telemetry.End(span, func() error { return rerr })
	schemaJSONFile, err := deps.SchemaIntrospectionJSONFile(ctx, []string{"Host"})
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self.Name(), err)
	}

	err = sdk.mod.dag.Select(ctx, sdk.mod.sdk, &inst,
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
