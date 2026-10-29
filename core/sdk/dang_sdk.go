package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	dangv2 "github.com/dagger/dagger/core/sdk/dang/v2"
	"github.com/dagger/dagger/dagql"
)

type dangSDK struct {
	root      *core.Query
	rawConfig map[string]any
}

func (sdk *dangSDK) CloneForModuleSource(*core.ModuleSource) core.SDK {
	if sdk == nil {
		return nil
	}
	cp := *sdk
	if sdk.rawConfig != nil {
		cp.rawConfig = make(map[string]any, len(sdk.rawConfig))
		for k, v := range sdk.rawConfig {
			cp.rawConfig[k] = v
		}
	}
	return &cp
}

// dangImpl is the Dang SDK implementation. It evaluates every Dang module with
// the one Dang library the engine links (github.com/vito/dang/v2), parsing
// each module's source as the Dang language version it is written in
// (ModuleSource.DangVersion: Dang 1.0 for modules from before Dang 2). Unlike
// core.ModuleTypes, its ModuleTypes takes already-scoped values: the scoping
// helpers are unexported in this package and dangv2 can't import it (cycle),
// so the SDK scopes before delegating.
var dangImpl = dangv2.Impl{}

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

func (sdk *dangSDK) AsModuleInitializer() (core.ModuleInitializer, bool) {
	return nil, false
}

func (sdk *dangSDK) AsClientInitializer() (core.ClientInitializer, bool) {
	return nil, false
}

func (sdk *dangSDK) AsRuntimeTarget() (core.RuntimeTarget, bool) {
	return nil, false
}

// AsModule reports no module: the Dang SDK is packaged into the engine rather
// than loaded as a Dagger module, so it exposes no generator functions.
func (sdk *dangSDK) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return dagql.ObjectResult[*core.Module]{}, false
}

func (sdk *dangSDK) AttachDependencyResults(
	context.Context,
	func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	return nil, nil
}

// AlwaysEnablesSelfCalls reports that the Dang SDK always needs self calls.
// Unlike compiled SDKs, the interpreter resolves its own types at runtime by
// name against the schema, so a module's own types must be present in the
// schema it queries — e.g. to call a method on an interface value received
// across a dependency. See isSelfCallsEnabled.
func (sdk *dangSDK) AlwaysEnablesSelfCalls() bool {
	return true
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
	return dangImpl.Runtime(ctx, deps, source)
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

	return dangImpl.ModuleTypes(ctx, deps, src, scopedMod)
}
