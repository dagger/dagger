package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
)

type moduleTypes struct {
	mod *module
}

const moduleIDPath = "/module-typedefs.json"

var moduleTypesExecMDDigest = digest.FromString("module-types-with-exec-execmd")

func (sdk *moduleTypes) ModuleTypes(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
	partiallyInitializedMod *core.Module,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "module SDK: load typedefs object")
	defer telemetry.EndWithCause(span, &rerr)

	dag := sdk.mod.dag()

	source, err := scopeSourceForSDKOperation(ctx, source, "moduleTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
	}
	scopedMod, err := ScopeModuleForSDKOperation(ctx, partiallyInitializedMod, "moduleTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
	}
	moduleContextID, err := core.ResultIDInput(scopedMod)
	if err != nil {
		return inst, fmt.Errorf("failed to get module context ID for sdk module %s moduleTypes: %w", sdk.mod.mod.Self().Name(), err)
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

	execMD := engineutil.ExecutionMetadata{
		ClientID: identity.NewID(),
		Internal: true,
	}
	if curCall := dagql.CurrentCall(ctx); curCall != nil {
		callDigest, err := curCall.RecipeDigest(ctx)
		if err != nil {
			return inst, fmt.Errorf("compute module types exec call digest: %w", err)
		}
		execMD.CallDigest = callDigest
	}
	if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
		execMD.LockMode = clientMetadata.LockMode
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

	hideCtx := dagql.WithSkip(ctx)
	err = dag.Select(hideCtx, ctr, &ctr,
		dagql.Selector{
			Field: "withExec",
			Args: []dagql.NamedInput{
				{Name: "args", Value: dagql.ArrayInput[dagql.String]{}},
				{Name: "useEntrypoint", Value: dagql.NewBoolean(true)},
				{Name: "experimentalPrivilegedNesting", Value: dagql.NewBoolean(true)},
				{Name: "execMD", Value: dagql.NewDigestedSerializedString(&execMD, moduleTypesExecMDDigest)},
				{Name: "moduleContext", Value: dagql.Opt(moduleContextID)},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to execute sdk moduleTypes container: %w", err)
	}

	var syncedCtrID dagql.ID[*core.Container]
	if err = dag.Select(hideCtx, ctr, &syncedCtrID, dagql.Selector{
		Field: "sync",
	}); err != nil {
		return inst, fmt.Errorf("failed to sync sdk moduleTypes container: %w", err)
	}

	ctr, err = syncedCtrID.Load(ctx, dag)
	if err != nil {
		return inst, fmt.Errorf("failed to load synced sdk moduleTypes container: %w", err)
	}

	var modDefsID string
	err = dag.Select(ctx, ctr, &modDefsID,
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
		return inst, fmt.Errorf("failed to decode module ID from type defs json: %w", err)
	}

	modCallID, err := modID.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get module ID handle from type defs json: %w", err)
	}
	inst, err = dagql.NewID[*core.Module](modCallID).Load(ctx, dag)
	if err != nil {
		return inst, fmt.Errorf("failed to load module from type defs json: %w", err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get engine cache for sdk moduleTypes dependency: %w", err)
	}
	if err := cache.AddExplicitDependency(ctx, ctr, inst, "sdk_module_types_generated_module"); err != nil {
		return inst, fmt.Errorf("failed to retain loaded module result from sdk moduleTypes exec: %w", err)
	}

	return inst, nil
}
