package sdk

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
)

// module is an SDK implemented as module; i.e. every module besides the special case go sdk.
type module struct {
	// The module implementing the SDK.
	mod dagql.ObjectResult[*core.Module]

	root *core.Query

	optionalFullSDKSourceDir dagql.ObjectResult[*core.Directory]
	rawConfig                map[string]any

	funcs map[string]*core.Function
}

type moduleInstance struct {
	dag *dagql.Server
	sdk dagql.AnyObjectResult
}

func newModuleSDK(
	ctx context.Context,
	root *core.Query,
	sdkModMeta dagql.ObjectResult[*core.Module],
	optionalFullSDKSourceDir dagql.ObjectResult[*core.Directory],
	rawConfig map[string]any,
) (*module, error) {
	sdk := &module{
		root:                     root,
		mod:                      sdkModMeta,
		optionalFullSDKSourceDir: optionalFullSDKSourceDir,
		rawConfig:                rawConfig,
		funcs:                    listImplementedFunctions(sdkModMeta.Self()),
	}

	if _, err := sdk.instantiate(ctx); err != nil {
		return nil, err
	}

	return sdk, nil
}

func (sdk *module) CloneForModuleSource(*core.ModuleSource) core.SDK {
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
	if sdk.funcs != nil {
		cp.funcs = make(map[string]*core.Function, len(sdk.funcs))
		for k, v := range sdk.funcs {
			cp.funcs[k] = v
		}
	}
	return &cp
}

func (sdk *module) instantiate(ctx context.Context) (*moduleInstance, error) {
	dag, err := dagql.NewServer(ctx, sdk.root)
	if err != nil {
		return nil, fmt.Errorf("create sdk module server: %w", err)
	}
	dag.Around(core.AroundFunc)
	core.InstallCoreSchemaLoaders(dag)

	if err := core.NewUserMod(sdk.mod).Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("failed to install sdk module %s: %w", sdk.mod.Self().Name(), err)
	}

	defaultDeps, err := sdk.root.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default deps for sdk module %s: %w", sdk.mod.Self().Name(), err)
	}
	for _, defaultDep := range defaultDeps.Mods() {
		if err := defaultDep.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("failed to install default dep %s for sdk module %s: %w", defaultDep.Name(), sdk.mod.Self().Name(), err)
		}
	}

	var sdkObj dagql.AnyObjectResult
	var constructorArgs []dagql.NamedInput
	if sdk.optionalFullSDKSourceDir.Self() != nil {
		sdkSourceDirID, err := sdk.optionalFullSDKSourceDir.ID()
		if err != nil {
			return nil, fmt.Errorf("failed to get full sdk source directory ID: %w", err)
		}
		constructorArgs = []dagql.NamedInput{
			{Name: "sdkSourceDir", Value: dagql.Opt(dagql.NewID[*core.Directory](sdkSourceDirID))},
		}
	}

	if err := dag.Select(ctx, dag.Root(), &sdkObj,
		dagql.Selector{
			Field: gqlFieldName(sdk.mod.Self().Name()),
			Args:  constructorArgs,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to get sdk object for sdk module %s: %w", sdk.mod.Self().Name(), err)
	}

	sdkObj, err = sdk.configuredSDK(ctx, dag, sdkObj)
	if err != nil {
		return nil, err
	}

	return &moduleInstance{dag: dag, sdk: sdkObj}, nil
}

// withConfig function checks if the moduleSDK exposes a function with name `WithConfig`.
//
// If the function with that name exists, it calls that function with arguments as read
// from dagger.json -> sdk.config object.
//
// Further, if the value for a specific arg for that function is not specified in dagger.json -> sdk.config object,
// the default value as specified in the moduleSource is used for that argument.
func (sdk *module) configuredSDK(
	ctx context.Context,
	dag *dagql.Server,
	sdkObj dagql.AnyObjectResult,
) (dagql.AnyObjectResult, error) {
	withConfigFn, ok := sdk.funcs["withConfig"]
	if !ok && len(sdk.rawConfig) > 0 {
		return nil, fmt.Errorf("sdk does not currently support specifying config")
	}

	if !ok {
		return sdkObj, nil
	}

	fieldspec, err := withConfigFn.FieldSpec(ctx, core.NewUserMod(sdk.mod))
	if err != nil {
		return nil, err
	}

	inputs := fieldspec.Args.Inputs(dag.View)

	// check if there are any unknown config keys provided
	var unusedKeys = []string{}
	for configKey := range sdk.rawConfig {
		found := false
		for _, input := range inputs {
			if input.Name == configKey {
				found = true
				break
			}
		}

		if !found {
			unusedKeys = append(unusedKeys, configKey)
		}
	}

	if len(unusedKeys) > 0 {
		return nil, fmt.Errorf("unknown sdk config keys found %v", unusedKeys)
	}
	args := []dagql.NamedInput{}
	for _, input := range inputs {
		var valInput = input.Default

		// override if the argument with same name exists in dagger.json -> sdk.config
		val, ok := sdk.rawConfig[input.Name]
		if ok && !input.Internal {
			valInput, err = input.Type.Decoder().DecodeInput(val)
			if err != nil {
				return nil, fmt.Errorf("parsing value for arg %q: %w", input.Name, err)
			}
		}

		args = append(args, dagql.NamedInput{
			Name:  input.Name,
			Value: valInput,
		})
	}

	var sdkwithconfig dagql.AnyObjectResult
	err = dag.Select(ctx, sdkObj, &sdkwithconfig, []dagql.Selector{
		{
			Field: "withConfig",
			Args:  args,
		},
	}...)
	if err != nil {
		return nil, fmt.Errorf("failed to call withConfig on the sdk module: %w", err)
	}

	return sdkwithconfig, nil
}

func (sdk *module) AttachDependencyResults(
	ctx context.Context,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if sdk == nil {
		return nil, nil
	}

	deps := make([]dagql.AnyResult, 0, 2)

	if sdk.mod.Self() != nil {
		attached, err := attach(sdk.mod)
		if err != nil {
			return nil, fmt.Errorf("attach sdk implementation module: %w", err)
		}
		mod, ok := attached.(dagql.ObjectResult[*core.Module])
		if !ok {
			return nil, fmt.Errorf("attach sdk implementation module: unexpected result %T", attached)
		}
		sdk.mod = mod
		sdk.funcs = listImplementedFunctions(mod.Self())
		deps = append(deps, mod)
	}

	if sdk.optionalFullSDKSourceDir.Self() != nil {
		attached, err := attach(sdk.optionalFullSDKSourceDir)
		if err != nil {
			return nil, fmt.Errorf("attach sdk source directory: %w", err)
		}
		dir, ok := attached.(dagql.ObjectResult[*core.Directory])
		if !ok {
			return nil, fmt.Errorf("attach sdk source directory: unexpected result %T", attached)
		}
		sdk.optionalFullSDKSourceDir = dir
		deps = append(deps, dir)
	}

	return deps, nil
}

func (sdk *module) AsRuntime() (core.Runtime, bool) {
	if _, ok := sdk.funcs["moduleRuntime"]; !ok {
		return nil, false
	}

	return &runtimeModule{mod: sdk}, true
}

func (sdk *module) AsModuleTypes() (core.ModuleTypes, bool) {
	if _, ok := sdk.funcs["moduleTypes"]; !ok {
		return nil, false
	}
	return &moduleTypes{mod: sdk}, true
}

func (sdk *module) AsCodeGenerator() (core.CodeGenerator, bool) {
	if _, ok := sdk.funcs["codegen"]; !ok {
		return nil, false
	}

	return &codeGeneratorModule{mod: sdk}, true
}

func (sdk *module) AsClientGenerator() (core.ClientGenerator, bool) {
	// We do not need to check if the SDK implements the
	// `requiredClientGenerationFiles` function, since it's
	// only useful if the client generator needs files from
	// the host so it may not be implemented.
	if _, ok := sdk.funcs["generateClient"]; !ok {
		return nil, false
	}

	return &clientGeneratorModule{mod: sdk, funcs: sdk.funcs}, true
}

func (sdk *module) AsModuleInitializer() (core.ModuleInitializer, bool) {
	if _, ok := sdk.funcs["initModule"]; !ok {
		return nil, false
	}

	return &moduleInitializerModule{mod: sdk, funcs: sdk.funcs}, true
}

func (sdk *module) AsClientInitializer() (core.ClientInitializer, bool) {
	if _, ok := sdk.funcs["initClient"]; !ok {
		return nil, false
	}

	return &clientInitializerModule{mod: sdk, funcs: sdk.funcs}, true
}

func (sdk *module) AsRuntimeTarget() (core.RuntimeTarget, bool) {
	if _, ok := sdk.funcs["targetRuntime"]; !ok {
		return nil, false
	}
	return sdk, true
}

// TargetRuntime invokes the SDK module's `targetRuntime` field. The field
// takes no arguments — it advertises which engine runtime the SDK's emitted
// code targets. Called once at `dagger module init` time; the returned
// value is written into the new module's dagger-module.toml `[runtime]
// source`.
func (sdk *module) TargetRuntime(ctx context.Context) (string, error) {
	sdkInst, err := sdk.instantiate(ctx)
	if err != nil {
		return "", fmt.Errorf("initialize sdk module %s targetRuntime: %w", sdk.mod.Self().Name(), err)
	}
	var out dagql.String
	if err := sdkInst.dag.Select(ctx, sdkInst.sdk, &out, dagql.Selector{
		Field: "targetRuntime",
	}); err != nil {
		return "", fmt.Errorf("call sdk %s targetRuntime: %w", sdk.mod.Self().Name(), err)
	}
	return out.String(), nil
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

// Return a map of all the functions implemented by the given SDK module.
func listImplementedFunctions(sdkMod *core.Module) map[string]*core.Function {
	result := make(map[string]*core.Function)

	for _, def := range sdkMod.ObjectDefs {
		// Skip if the object isn't valid.
		if !def.Self().AsObject.Valid {
			continue
		}

		// Skip if it's not the main object.
		obj := def.Self().AsObject.Value.Self()
		if gqlFieldName(obj.Name) != gqlFieldName(sdkMod.NameField) {
			continue
		}

		// Loop through the main object functions and look
		// for a match with the interface functions.
		for _, fn := range obj.Functions {
			fnSelf := fn.Self()
			for _, name := range sdkFunctions {
				if gqlFieldName(fnSelf.Name) == gqlFieldName(name) {
					result[name] = fnSelf
				}
			}
		}

		// Once we looped through the main object functions, we can break the loop.
		break
	}

	return result
}
