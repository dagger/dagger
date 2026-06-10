package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	dangv1 "github.com/dagger/dagger/core/sdk/dang/v1"
	dangv2 "github.com/dagger/dagger/core/sdk/dang/v2"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type dangSDK struct {
	root      *core.Query
	rawConfig map[string]any
}

// dangImpl is implemented by each supported Dang major version
// (core/sdk/dang/v1, v2, ...). Unlike core.ModuleTypes, ModuleTypes takes
// already-scoped values: the scoping helpers are unexported in this package
// and the version packages can't import it (cycle), so the dispatcher scopes
// before delegating.
type dangImpl interface {
	ModuleTypes(
		ctx context.Context,
		deps *core.SchemaBuilder,
		scopedSrc dagql.ObjectResult[*core.ModuleSource],
		scopedMod dagql.ObjectResult[*core.Module],
	) (dagql.ObjectResult[*core.Module], error)

	Runtime(
		ctx context.Context,
		deps *core.SchemaBuilder,
		source dagql.ObjectResult[*core.ModuleSource],
	) (core.ModuleRuntime, error)
}

// dangImplFor picks the Dang major version matching the module's engine
// version: modules pinned before a major's gate keep the semantics they were
// written against. Newest-first ladder; adding a future major is one case.
func dangImplFor(src *core.ModuleSource) dangImpl {
	if engine.CheckVersionCompatibility(
		engine.BaseVersion(engine.NormalizeVersion(src.EngineVersion)),
		engine.MinimumDangV2ModuleVersion,
	) {
		return dangv2.Impl{}
	}
	return dangv1.Impl{}
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

func (sdk *dangSDK) AttachDependencyResults(
	context.Context,
	func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	return nil, nil
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
	return dangImplFor(source.Self()).Runtime(ctx, deps, source)
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

	return dangImplFor(src.Self()).ModuleTypes(ctx, deps, src, scopedMod)
}
