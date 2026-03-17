package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/identity"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
)

type moduleTypes struct {
	mod *module
}

const (
	moduleIDPath = "/module-typedefs.json"
)

var moduleTypesExecMDDigest = digest.FromString("module-types-with-exec-execmd")

func (sdk *moduleTypes) ModuleTypes(
	ctx context.Context,
	deps *core.ModDeps,
	source dagql.ObjectResult[*core.ModuleSource],
	partiallyInitializedMod *core.Module,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load typedefs object")
	defer telemetry.EndWithCause(span, &rerr)

	dag, err := sdk.mod.dag(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.mod.Self().Name(), err)
	}

	source, err = scopeSourceForSDKOperation(ctx, source, "moduleTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
	}
	scopedMod, err := ScopeModuleForSDKOperation(ctx, partiallyInitializedMod, "moduleTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
	}
	currentModuleID, err := scopedMod.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get current module ID for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}
	sourceID, err := source.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get scoped module source ID for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
	}
	schemaJSONFileID, err := schemaJSONFile.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json ID during %s module sdk runtime: %w", sdk.mod.mod.Self().Name(), err)
	}

	execMD := buildkit.ExecutionMetadata{
		ClientID: identity.NewID(),
		Call:     dagql.CurrentCall(ctx),
		ExecID:   identity.NewID(),
		Internal: true,
	}
	if execMD.Call != nil {
		callDigest, err := execMD.Call.RecipeDigest()
		if err != nil {
			return inst, fmt.Errorf("compute module types exec call digest: %w", err)
		}
		execMD.CallDigest = callDigest
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
					Value: dagql.NewID[*core.ModuleSource](sourceID),
				},
				{
					Name:  "introspectionJson",
					Value: dagql.NewID[*core.File](schemaJSONFileID),
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
	err = dag.Select(dagql.WithSkip(ctx), ctr, &modDefsID,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{Name: "args", Value: dagql.ArrayInput[dagql.String]{}},
				{Name: "useEntrypoint", Value: dagql.NewBoolean(true)},
				{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(true)},
				{Name: "execMD", Value: dagql.NewDigestedSerializedString(&execMD, moduleTypesExecMDDigest)},
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

	modCallID, err := modID.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module ID handle from type defs json: %w", err)
	}
	err = dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{
			Field: "loadModuleFromID",
			Args: []dagql.NamedInput{
				{
					Name:  "id",
					Value: dagql.NewID[*core.Module](modCallID),
				},
			},
		})
	if err != nil {
		return inst, fmt.Errorf("failed to load module from type defs json: %w", err)
	}

	return inst, nil
}
