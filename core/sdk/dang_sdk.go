package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/pkg/dang"
)

type dangSDK struct {
	root      *core.Query
	rawConfig map[string]any
}

func (sdk *dangSDK) AsRuntime() (core.Runtime, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsModuleTypes() (core.ModuleTypes, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsCodeGenerator() (core.CodeGenerator, bool) {
	return sdk, true
}

func (sdk *dangSDK) AsClientGenerator() (core.ClientGenerator, bool) {
	return sdk, true
}

func (sdk *dangSDK) RequiredClientGenerationFiles(_ context.Context) (dagql.Array[dagql.String], error) {
	return dagql.NewStringArray(), nil
}

func (sdk *dangSDK) GenerateClient(
	ctx context.Context,
	modSource dagql.ObjectResult[*core.ModuleSource],
	schemaJSONFile dagql.Result[*core.File],
	outputDir string,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	return inst, fmt.Errorf("dang SDK does not have a client to generate")
}

func (sdk *dangSDK) Codegen(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ *core.GeneratedCode, rerr error) {
	return &core.GeneratedCode{
		// no-op
		Code: source.Self().ContextDirectory,
	}, nil
}

func (sdk *dangSDK) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (core.ModuleRuntime, error) {
	return &DangRuntime{
		deps:      deps,
		modSource: source,
	}, nil
}

func (sdk *dangSDK) ModuleTypes(
	ctx context.Context,
	deps *core.SchemaBuilder,
	src dagql.ObjectResult[*core.ModuleSource],
	partiallyInitializedMod *core.Module,
) (inst dagql.ObjectResult[*core.Module], rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag for dang module sdk module types: %w", err)
	}

	src, err = scopeSourceForSDKOperation(ctx, src, "moduleTypes", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module source for dang module sdk module types: %w", err)
	}

	scopedMod, err := ScopeModuleForSDKOperation(ctx, partiallyInitializedMod, "dangSDK", dag)
	if err != nil {
		return inst, fmt.Errorf("failed to scope module for dang module sdk module types: %w", err)
	}

	schemaJSONFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get schema introspection json during dang module sdk module types: %w", err)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, fmt.Errorf("current query: %w", err)
	}

	clientMetadata, nestedClientMetadata, err := newDangNestedClientMetadata(ctx)
	if err != nil {
		return inst, err
	}

	runner := dangSourceRunner(func(ctx context.Context, modSrcDir string) (dang.EvalEnv, error) {
		return dang.RunDir(ctx, modSrcDir, false)
	})
	if src.Self().SDK.ExperimentalFeatureEnabled(core.ModuleSourceExperimentalFeatureSelfCalls) {
		runner = runDangDirForModuleTypes
	}

	_, err = evalDangSource(ctx, query, src, schemaJSONFile, nestedClientMetadata, clientMetadata.ClientID, true, nil, scopedMod, dagql.ObjectResult[*core.Env]{}, runner, func(ctx context.Context, env dang.EvalEnv) ([]byte, error) {
		inst, err = initDangModule(ctx, dag, env)
		if err != nil {
			return nil, fmt.Errorf("init module: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func newDangNestedClientMetadata(ctx context.Context) (*engine.ClientMetadata, *engine.ClientMetadata, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	nestedClientMetadata := &engine.ClientMetadata{
		ClientID:          identity.NewID(),
		ClientSecretToken: identity.NewID(),
		SessionID:         clientMetadata.SessionID,
		ClientStableID:    identity.NewID(),
		ClientVersion:     engine.Version,
		AllowedLLMModules: slices.Clone(clientMetadata.AllowedLLMModules),
		LockMode:          clientMetadata.LockMode,
		WorkspaceEnv:      clientMetadata.WorkspaceEnv,
	}

	return clientMetadata, nestedClientMetadata, nil
}

// DangRuntime is a native Dang runtime that doesn't use containers
type DangRuntime struct {
	deps      *core.SchemaBuilder
	modSource dagql.ObjectResult[*core.ModuleSource]
}

func (r *DangRuntime) AsContainer() (dagql.ObjectResult[*core.Container], bool) {
	// Dang runtime doesn't use containers
	return dagql.ObjectResult[*core.Container]{}, false
}

func (r *DangRuntime) Call(
	ctx context.Context,
	_ *engineutil.ExecutionMetadata,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
	envContext dagql.ObjectResult[*core.Env],
) (res []byte, rerr error) {
	defer func() {
		if rerr != nil {
			rerr = convertError(rerr)
		}
	}()

	clientMetadata, nestedClientMetadata, err := newDangNestedClientMetadata(ctx)
	if err != nil {
		return nil, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("current query: %w", err)
	}
	schemaJSONFile, err := r.deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("get schema introspection: %w", err)
	}
	outputBytes, err := r.eval(ctx, query, schemaJSONFile, nestedClientMetadata, clientMetadata.ClientID, true, fnCall, moduleContext, envContext)
	if err != nil {
		return nil, err
	}
	return outputBytes, nil
}

func convertError(rerr error) *core.Error {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		dagErr := core.NewError(gqlErr.Message)
		if gqlErr.Extensions != nil {
			keys := make([]string, 0, len(gqlErr.Extensions))
			for k := range gqlErr.Extensions {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val, err := json.Marshal(gqlErr.Extensions[k])
				if err != nil {
					fmt.Println("failed to marshal error value:", err)
				}
				dagErr = dagErr.WithValue(k, core.JSON(val))
			}
		}
		return dagErr
	}
	return core.NewError(rerr.Error())
}
