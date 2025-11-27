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

	// The SDK object retrieved from the server, for calling functions against.
	sdk dagql.AnyObjectResult

	// A server that the SDK module has been installed to.
	serverSchema *dagql.ServerSchema

	funcs map[string]*core.Function
}

func newModuleSDK(
	ctx context.Context,
	root *core.Query,
	sdkModMeta dagql.ObjectResult[*core.Module],
	rawConfig map[string]any,
) (*module, error) {
	dagqlCache, err := root.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache for sdk module %s: %w", sdkModMeta.Self().Name(), err)
	}
	dag := dagql.NewServer(root, dagqlCache)
	dag.Around(core.AroundFunc)

	if err := sdkModMeta.Self().Install(ctx, dag); err != nil {
		return nil, fmt.Errorf("failed to install sdk module %s: %w", sdkModMeta.Self().Name(), err)
	}

	defaultDeps, err := root.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default deps for sdk module %s: %w", sdkModMeta.Self().Name(), err)
	}
	for _, defaultDep := range defaultDeps.Mods {
		if err := defaultDep.Install(ctx, dag); err != nil {
			return nil, fmt.Errorf("failed to install default dep %s for sdk module %s: %w", defaultDep.Name(), sdkModMeta.Self().Name(), err)
		}
	}

	env, err := sdkModMeta.Self().Env(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get env for sdk module %s: %w", sdkModMeta.Self().Name(), err)
	}

	var sdk dagql.AnyObjectResult
	if err := dag.Select(core.EnvIDToContext(ctx, env.ID()), dag.Root(), &sdk,
		dagql.Selector{
			Field: gqlFieldName(sdkModMeta.Self().Name()),
		},
	); err != nil {
		return nil, fmt.Errorf("failed to get sdk object for sdk module %s: %w", sdkModMeta.Self().Name(), err)
	}

	return (&module{
		mod:          sdkModMeta,
		serverSchema: dag.AsSchema(),
		sdk:          sdk,
		funcs:        listImplementedFunctions(sdkModMeta.Self()),
	}).withConfig(ctx, rawConfig)
}

func (sdk *module) dag(ctx context.Context) (*dagql.Server, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	dagqlCache, err := query.Cache(ctx)
	if err != nil {
		return nil, err
	}
	return sdk.serverSchema.WithCache(dagqlCache), nil
}

// withConfig function checks if the moduleSDK exposes a function with name `WithConfig`.
//
// If the function with that name exists, it calls that function with arguments as read
// from dagger.json -> sdk.config object.
//
// Further, if the value for a specific arg for that function is not specified in dagger.json -> sdk.config object,
// the default value as specified in the moduleSource is used for that argument.
func (sdk *module) withConfig(
	ctx context.Context,
	rawConfig map[string]any,
) (*module, error) {
	withConfigFn, ok := sdk.funcs["withConfig"]
	if !ok && len(rawConfig) > 0 {
		return sdk, fmt.Errorf("sdk does not currently support specifying config")
	}

	if !ok {
		return sdk, nil
	}

	fieldspec, err := withConfigFn.FieldSpec(ctx, sdk.mod.Self())
	if err != nil {
		return nil, err
	}

	inputs := fieldspec.Args.Inputs(sdk.serverSchema.View())

	// check if there are any unknown config keys provided
	var unusedKeys = []string{}
	for configKey := range rawConfig {
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
		val, ok := rawConfig[input.Name]
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

	dag, err := sdk.dag(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for sdk module %s: %w", sdk.mod.Self().Name(), err)
	}

	var sdkwithconfig dagql.AnyObjectResult
	err = dag.Select(ctx, sdk.sdk, &sdkwithconfig, []dagql.Selector{
		{
			Field: "withConfig",
			Args:  args,
		},
	}...)
	if err != nil {
		return nil, fmt.Errorf("failed to call withConfig on the sdk module: %w", err)
	}

	sdk.sdk = sdkwithconfig

	return sdk, nil
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

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

// Return a map of all the functions implemented by the given SDK module.
func listImplementedFunctions(sdkMod *core.Module) map[string]*core.Function {
	result := make(map[string]*core.Function)

	for _, def := range sdkMod.ObjectDefs {
		// Skip if the object isn't valid.
		if !def.AsObject.Valid {
			continue
		}

		// Skip if it's not the main object.
		obj := def.AsObject.Value
		if gqlFieldName(obj.Name) != gqlFieldName(sdkMod.NameField) {
			continue
		}

		// Loop through the main object functions and look
		// for a match with the interface functions.
		for _, fn := range obj.Functions {
			for _, name := range sdkFunctions {
				if gqlFieldName(fn.Name) == gqlFieldName(name) {
					result[name] = fn
				}
			}
		}

		// Once we looped through the main object functions, we can break the loop.
		break
	}

	return result
}
