package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// module is an SDK implemented as module; i.e. every module besides the special case go sdk.
type module struct {
	// The module implementing the SDK.
	mod dagql.ObjectResult[*core.Module]

	root *core.Query

	optionalFullSDKSourceDir dagql.ObjectResult[*core.Directory]
	rawConfig                map[string]any

	funcs map[string]*core.Function

	moduleSDKOpts
}

// moduleSDKOpts are what an SDK the engine carries needs of it beyond a
// module SDK's own.
type moduleSDKOpts struct {
	// builtinArgs are constructor arguments the engine gives the SDK's
	// module: what it knows of the engine and the calling client that a
	// module can't reach itself. They depend on nothing but the session and
	// the client, which instanceKey keys by.
	builtinArgs func(context.Context) ([]dagql.NamedInput, error)
	// selfCalls reports that the SDK's modules always enable self calls
	// (core.ModuleSource.SelfCallsEnabled).
	selfCalls bool
	// generatedFiles are the files, from a module's source directory, that a
	// module building from committed files must have. Without one, loading it
	// fails with core.MissingGeneratedFileError, which callers tell apart from
	// a failed build, as a module can't.
	generatedFiles []string
	// debugTerminal opens a terminal in a module's runtime container when
	// its SDK config sets debug, as the built-in Go SDK did.
	debugTerminal bool
	// moduleTypes reports a module's types without its runtime, in the engine.
	// Such a module has no reason to build while it loads.
	moduleTypes core.ModuleTypes
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
	return newModuleSDKWith(ctx, root, sdkModMeta, optionalFullSDKSourceDir, rawConfig, moduleSDKOpts{})
}

func newModuleSDKWith(
	ctx context.Context,
	root *core.Query,
	sdkModMeta dagql.ObjectResult[*core.Module],
	optionalFullSDKSourceDir dagql.ObjectResult[*core.Directory],
	rawConfig map[string]any,
	opts moduleSDKOpts,
) (*module, error) {
	sdk := &module{
		root:                     root,
		mod:                      sdkModMeta,
		optionalFullSDKSourceDir: optionalFullSDKSourceDir,
		rawConfig:                rawConfig,
		funcs:                    listImplementedFunctions(sdkModMeta.Self()),
		moduleSDKOpts:            opts,
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

// argName is the schema name the SDK module gives an argument of one of the
// SDK interface functions, by the naming rules of its engine version. The
// rules agree on introspectionJson: strcase lowercases the acronym, and
// engine/naming writes field acronyms like words.
func (sdk *module) argName(name string) string {
	return sdk.mod.Self().Namer().ArgName(name)
}

// RuntimeTrustsCommittedFiles reports whether the SDK runtime can build the
// module from committed generated files, without the introspection JSON. An
// SDK declares it by making moduleRuntime's introspectionJson arg optional.
func (sdk *module) RuntimeTrustsCommittedFiles() bool {
	fn, ok := sdk.funcs["moduleRuntime"]
	if !ok {
		return false
	}
	for _, arg := range fn.Args {
		if arg.Self() == nil || arg.Self().Name != sdk.argName(introspectionJSONArgName) {
			continue
		}
		typeDef := arg.Self().TypeDef
		return typeDef.Self() != nil && typeDef.Self().Optional
	}
	return false
}

// instantiate serves the SDK module, configured, in a server of its own.
// Serving it costs more than a call to it whose result is cached, and every
// module of a session calls its SDK several times (types, codegen, runtime),
// so a session serves each SDK once, as SchemaBuilderMemo builds a session's
// module schemas once. The server is only selected from.
func (sdk *module) instantiate(ctx context.Context) (*moduleInstance, error) {
	key, ok, err := sdk.instanceKey(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return sdk.newInstance(ctx)
	}
	return sessionModuleInstance(ctx, key, sdk.newInstance)
}

// instanceKey identifies the SDK instance this module SDK serves within the
// session: its module and full source directory by attached result, and its
// configuration. ok is false when the context has no session or a result
// isn't attached, which leaves nothing to key by.
func (sdk *module) instanceKey(ctx context.Context) (string, bool, error) {
	metadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || metadata.SessionID == "" {
		return "", false, nil //nolint:nilerr // no session: serve without the session's cache
	}
	modID, ok := attachedResultID(sdk.mod)
	if !ok {
		return "", false, nil
	}
	var srcDirID uint64
	if sdk.optionalFullSDKSourceDir.Self() != nil {
		if srcDirID, ok = attachedResultID(sdk.optionalFullSDKSourceDir); !ok {
			return "", false, nil
		}
	}
	key, err := moduleInstanceKey(metadata.SessionID, sdk.root, modID, srcDirID, sdk.rawConfig)
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

// attachedResultID is res's engine result ID, or false when it has none.
func attachedResultID(res dagql.AnyResult) (uint64, bool) {
	id, err := res.ID()
	if err != nil || id == nil || !id.IsHandle() || id.EngineResultID() == 0 {
		return 0, false
	}
	return id.EngineResultID(), true
}

// moduleInstanceKey is the session cache key of an SDK instance. The cache
// finds entries by key alone, so the key carries the session, and the root,
// as a SchemaBuilderMemo keys a client's servers.
func moduleInstanceKey(sessionID string, root *core.Query, modID, srcDirID uint64, config map[string]any) (string, error) {
	// encoding/json writes a map's keys sorted, so equal configs key alike.
	cfg, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("sdk config: %w", err)
	}
	return fmt.Sprintf("module-sdk-instance:%s:%p:%d:%d:%s", sessionID, root, modID, srcDirID, cfg), nil
}

// sessionModuleInstance is the session's SDK instance for key, made by
// build the first time the session asks for it. A failed build isn't kept,
// so the next call builds again.
func sessionModuleInstance(ctx context.Context, key string, build func(context.Context) (*moduleInstance, error)) (*moduleInstance, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return build(ctx)
	}
	metadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || metadata.SessionID == "" {
		return build(ctx)
	}
	res, err := cache.GetOrInitArbitrary(ctx, metadata.SessionID, key, func(ctx context.Context) (any, error) {
		return build(ctx)
	})
	if err != nil {
		return nil, err
	}
	inst, ok := res.Value().(*moduleInstance)
	if !ok {
		return nil, fmt.Errorf("sdk instance: unexpected %T", res.Value())
	}
	return inst, nil
}

// newInstance serves the SDK module, configured, in a new server.
func (sdk *module) newInstance(ctx context.Context) (*moduleInstance, error) {
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
	if sdk.builtinArgs != nil {
		args, err := sdk.builtinArgs(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get constructor arguments for sdk module %s: %w", sdk.mod.Self().Name(), err)
		}
		constructorArgs = append(constructorArgs, args...)
	}

	if err := dag.Select(ctx, dag.Root(), &sdkObj,
		dagql.Selector{
			Field: sdk.mod.Self().ConstructorName(),
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

// AlwaysEnablesSelfCalls reports whether the SDK's modules always enable self
// calls, as the Go SDK's do.
func (sdk *module) AlwaysEnablesSelfCalls() bool {
	return sdk.selfCalls
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
	if sdk.moduleSDKOpts.moduleTypes != nil {
		return sdk.moduleSDKOpts.moduleTypes, true
	}
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

func (sdk *module) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return sdk.mod, true
}

func (sdk *module) AsRuntimeTarget() (core.RuntimeTarget, bool) {
	if _, ok := sdk.funcs["targetRuntime"]; !ok {
		return nil, false
	}
	return sdk, true
}

// TargetRuntime invokes the SDK module's `targetRuntime` field. The field
// takes no arguments — it advertises which engine runtime the SDK's emitted
// code targets. Called once at `dagger module init <SDK>` time; the returned
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

// Return a map of all the functions implemented by the given SDK module.
func listImplementedFunctions(sdkMod *core.Module) map[string]*core.Function {
	result := make(map[string]*core.Function)
	namer := sdkMod.Namer()

	for _, def := range sdkMod.ObjectDefs {
		// Skip if the object isn't valid.
		if !def.Self().AsObject.Valid {
			continue
		}

		// Skip if it's not the main object.
		obj := def.Self().AsObject.Value.Self()
		sameName := namer.FieldName(obj.Name) == namer.FieldName(sdkMod.NameField) ||
			!namer.Legacy() && namer.SameDeclaredName(obj.Name, sdkMod.NameField)
		if !sameName {
			continue
		}

		// Loop through the main object functions and look
		// for a match with the interface functions.
		for _, fn := range obj.Functions {
			fnSelf := fn.Self()
			for _, name := range sdkFunctions {
				if namer.FieldName(fnSelf.Name) == namer.FieldName(name) {
					result[name] = fnSelf
				}
			}
		}

		// Once we looped through the main object functions, we can break the loop.
		break
	}

	return result
}

// HasInitializer reports whether a module implements either SDK initializer.
func HasInitializer(sdkMod *core.Module) bool {
	functions := listImplementedFunctions(sdkMod)
	return functions["initModule"] != nil || functions["initClient"] != nil
}
