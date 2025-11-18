package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

type moduleTypes struct {
	mod *module
}

const (
	moduleIDPath = "/module-typedefs.json"
)

func (sdk *moduleTypes) ModuleTypes(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
	currentModuleID *call.ID,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load typedefs object")
	defer telemetry.EndWithCause(span, &rerr)

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	execMD := buildkit.ExecutionMetadata{
		ClientID: identity.NewID(),
		CallID:   dagql.CurrentID(ctx),
		ExecID:   identity.NewID(),
		Internal: true,
	}
	execMD.EncodedModuleID, err = currentModuleID.Encode()
	if err != nil {
		return inst, err
	}

	var ctr dagql.ObjectResult[*core.Container]
	err = dag.Select(ctx, sdk.mod.sdk, &ctr,
		dagql.Selector{
			Field: "moduleTypes",
			Args: []dagql.NamedInput{
				{
					Name:  "modSource",
					Value: dagql.NewID[*core.ModuleSource](source.ID()),
				},
				{
					Name:  "introspectionJson",
					Value: dagql.NewID[*core.File](schemaJSONFile.ID()),
				},
				{
					Name:  "outputFilePath",
					Value: dagql.NewString(moduleIDPath),
				},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to call sdk moduleTypes: %w", err)
	}

	var modDefsID string
	ignoreCtx := dagql.WithSkip(ctx) // ignore some spans as they are internal trick only
	err = dag.Select(ignoreCtx, ctr, &modDefsID,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{Name: "args", Value: dagql.ArrayInput[dagql.String]{}},
				{Name: "useEntrypoint", Value: dagql.NewBoolean(true)},
				{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(true)},
				{Name: "execMD", Value: dagql.NewSerializedString(&execMD)},
			},
		},
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{
					Name:  "path",
					Value: dagql.NewString(moduleIDPath),
				},
			},
		},
		dagql.Selector{
			Field: "contents",
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to get type defs json during module sdk codegen: %w", err)
	}

	var modID core.ModuleID
	if err = json.Unmarshal([]byte(modDefsID), &modID); err != nil {
		return inst, err
	}

	err = dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{
			Field: "loadModuleFromID",
			Args: []dagql.NamedInput{
				{
					Name:  "id",
					Value: dagql.NewID[*core.Module](modID.ID()),
				},
			},
		})
	if err != nil {
		return inst, fmt.Errorf("failed to load module from type defs json: %w", err)
	}

	return inst, nil
}
