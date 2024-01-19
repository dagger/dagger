package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"golang.org/x/sync/errgroup"
)

type moduleSchema struct {
	dag *dagql.Server
}

var _ SchemaResolvers = &moduleSchema{}

func (s *moduleSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("module", s.module).
			Doc(`Create a new module.`),

		dagql.Func("typeDef", s.typeDef).
			Doc(`Create a new TypeDef.`),

		dagql.Func("generatedCode", s.generatedCode).
			Doc(`Create a code generation result, given a directory containing the generated code.`),

		dagql.Func("moduleSource", s.moduleSource).
			Doc(`Create a new module source instance from a source ref string.`).
			ArgDoc("refString", `The string ref representation of the module source`).
			ArgDoc("rootDirectory", `An explicitly set root directory for the module source. This is required to load local sources as modules, other source types implicitly encode the root directory and do not require this.`).
			ArgDoc("stable", `If true, enforce that the source is a stable version for source kinds that support versioning.`),

		dagql.Func("function", s.function).
			Doc(`Creates a function.`).
			ArgDoc("name", `Name of the function, in its original format from the implementation language.`).
			ArgDoc("returnType", `Return type of the function.`),

		dagql.Func("currentModule", s.currentModule).
			Impure(`Changes depending on which module is calling it.`).
			Doc(`The module currently being served in the session, if any.`),

		dagql.Func("currentTypeDefs", s.currentTypeDefs).
			Impure(`Changes depending on which modules are currently installed.`).
			Doc(`The TypeDef representations of the objects currently being served in the session.`),

		dagql.Func("currentFunctionCall", s.currentFunctionCall).
			Impure(`Changes depending on which function calls it.`).
			Doc(`The FunctionCall context that the SDK caller is currently executing in.`,
				`If the caller is not currently executing in a function, this will
				return an error.`),
	}.Install(s.dag)

	dagql.Fields[*core.Directory]{
		dagql.NodeFunc("asModule", s.directoryAsModule).
			Doc(`Load the directory as a Dagger module`).
			ArgDoc("sourceSubpath",
				`An optional subpath of the directory which contains the module's source code.`,
				`This is needed when the module code is in a subdirectory but requires
				parent directories to be loaded in order to execute. For example, the
				module source code may need a go.mod, project.toml, package.json, etc.
				file from a parent directory.`,
				`If not set, the module source code is loaded from the root of the directory.`),
	}.Install(s.dag)

	dagql.Fields[*core.FunctionCall]{
		dagql.Func("returnValue", s.functionCallReturnValue).
			Impure(`Updates internal engine state with the given value.`).
			Doc(`Set the return value of the function call to the provided value.`).
			ArgDoc("value", `JSON serialization of the return value.`),
	}.Install(s.dag)

	dagql.Fields[*core.ModuleSource]{
		dagql.Func("subpath", s.moduleSourceSubpath).
			Doc(`The path to the module subdirectory containing the actual module's source code.`),

		dagql.Func("asString", s.moduleSourceAsString).
			Doc(`A human readable ref string to this module source.`),

		dagql.NodeFunc("resolveDependency", s.moduleSourceResolveDependency).
			Doc(`Resolve the provided module source arg as a dependency relative to this module source.`).
			ArgDoc("dep", `The dependency module source to resolve.`),

		dagql.NodeFunc("asModule", s.moduleSourceAsModule).
			Doc(`Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation`),
	}.Install(s.dag)

	dagql.Fields[*core.LocalModuleSource]{}.Install(s.dag)

	dagql.Fields[*core.GitModuleSource]{
		dagql.Func("cloneURL", s.gitModuleSourceCloneURL).
			Doc(`The URL from which the source's git repo can be cloned from`),

		dagql.Func("htmlURL", s.gitModuleSourceHTMLURL).
			Doc(`The URL to the source's git repo in a web browser`),
	}.Install(s.dag)

	dagql.Fields[*core.ModuleDependency]{}.Install(s.dag)

	dagql.Fields[*core.Module]{
		dagql.NodeFunc("withSource", s.moduleWithSource).
			Doc(`Retrieves the module with basic configuration loaded, ready for initialization.`).
			ArgDoc("source", `The module source to initialize from.`),

		dagql.NodeFunc("initialize", s.moduleInitialize).
			Doc(`Retrieves the module with the objects loaded via its SDK.`),

		dagql.Func("withDescription", s.moduleWithDescription).
			Doc(`Retrieves the module with the given description`).
			ArgDoc("description", `The description to set`),

		dagql.Func("withObject", s.moduleWithObject).
			Doc(`This module plus the given Object type and associated functions.`),

		dagql.Func("withInterface", s.moduleWithInterface).
			Doc(`This module plus the given Interface type and associated functions`),

		dagql.Func("withName", s.moduleWithName).
			Doc(`Update the module configuration to use the given name.`).
			ArgDoc("name", `The name to use.`),

		dagql.Func("withSDK", s.moduleWithSDK).
			Doc(`Update the module configuration to use the given sdk.`).
			ArgDoc("sdk", `The sdk to use.`),

		dagql.NodeFunc("withDependencies", s.moduleWithDependencies).
			Doc(`Update the module configuration to use the given dependencies.`).
			ArgDoc("dependencies", `The module sources of dependencies to use.`),

		dagql.Func("generatedSourceDirectory", s.moduleGeneratedSourceDirectory).
			Doc(
				`The module's root directory containing the config file for it and its source`,
				`(possibly as a subdir). It includes any generated code or updated config files`,
				`created after initial load.`,
			),

		dagql.NodeFunc("serve", s.moduleServe).
			Impure(`Mutates the calling session's global schema.`).
			Doc(`Serve a module's API in the current session.`,
				`Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.`),
	}.Install(s.dag)

	dagql.Fields[*core.Function]{
		dagql.Func("withDescription", s.functionWithDescription).
			Doc(`Returns the function with the given doc string.`).
			ArgDoc("description", `The doc string to set.`),

		dagql.Func("withArg", s.functionWithArg).
			Doc(`Returns the function with the provided argument`).
			ArgDoc("name", `The name of the argument`).
			ArgDoc("typeDef", `The type of the argument`).
			ArgDoc("description", `A doc string for the argument, if any`).
			ArgDoc("defaultValue", `A default value to use for this argument if not explicitly set by the caller, if any`),
	}.Install(s.dag)

	dagql.Fields[*core.FunctionArg]{}.Install(s.dag)

	dagql.Fields[*core.FunctionCallArgValue]{}.Install(s.dag)

	dagql.Fields[*core.TypeDef]{
		dagql.Func("withOptional", s.typeDefWithOptional).
			Doc(`Sets whether this type can be set to null.`),

		dagql.Func("withKind", s.typeDefWithKind).
			Doc(`Sets the kind of the type.`),

		dagql.Func("withListOf", s.typeDefWithListOf).
			Doc(`Returns a TypeDef of kind List with the provided type for its elements.`),

		dagql.Func("withObject", s.typeDefWithObject).
			Doc(`Returns a TypeDef of kind Object with the provided name.`,
				`Note that an object's fields and functions may be omitted if the
				intent is only to refer to an object. This is how functions are able to
				return their own object, or any other circular reference.`),

		dagql.Func("withInterface", s.typeDefWithInterface).
			Doc(`Returns a TypeDef of kind Interface with the provided name.`),

		dagql.Func("withField", s.typeDefWithObjectField).
			Doc(`Adds a static field for an Object TypeDef, failing if the type is not an object.`).
			ArgDoc("name", `The name of the field in the object`).
			ArgDoc("typeDef", `The type of the field`).
			ArgDoc("description", `A doc string for the field, if any`),

		dagql.Func("withFunction", s.typeDefWithFunction).
			Doc(`Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.`),

		dagql.Func("withConstructor", s.typeDefWithObjectConstructor).
			Doc(`Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.`),
	}.Install(s.dag)

	dagql.Fields[*core.ObjectTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.InterfaceTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.InputTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.FieldTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.ListTypeDef]{}.Install(s.dag)

	dagql.Fields[*core.GeneratedCode]{
		dagql.Func("withVCSGeneratedPaths", s.generatedCodeWithVCSGeneratedPaths).
			Doc(`Set the list of paths to mark generated in version control.`),
	}.Install(s.dag)
}

func (s *moduleSchema) typeDef(ctx context.Context, _ *core.Query, args struct{}) (*core.TypeDef, error) {
	return &core.TypeDef{}, nil
}

func (s *moduleSchema) typeDefWithOptional(ctx context.Context, def *core.TypeDef, args struct {
	Optional bool
}) (*core.TypeDef, error) {
	return def.WithOptional(args.Optional), nil
}

func (s *moduleSchema) typeDefWithKind(ctx context.Context, def *core.TypeDef, args struct {
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	return def.WithKind(args.Kind), nil
}

func (s *moduleSchema) typeDefWithListOf(ctx context.Context, def *core.TypeDef, args struct {
	ElementType core.TypeDefID
}) (*core.TypeDef, error) {
	elemType, err := args.ElementType.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithListOf(elemType.Self), nil
}

func (s *moduleSchema) typeDefWithObject(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("object type def must have a name")
	}
	return def.WithObject(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithInterface(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
}) (*core.TypeDef, error) {
	return def.WithInterface(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string `default:""`
}) (*core.TypeDef, error) {
	fieldType, err := args.TypeDef.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithObjectField(args.Name, fieldType.Self, args.Description)
}

func (s *moduleSchema) typeDefWithFunction(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	fn, err := args.Function.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithFunction(fn.Self)
}

func (s *moduleSchema) typeDefWithObjectConstructor(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	inst, err := args.Function.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	fn := inst.Self.Clone()
	// Constructors are invoked by setting the ObjectName to the name of the object its constructing and the
	// FunctionName to "", so ignore the name of the function.
	fn.Name = ""
	fn.OriginalName = ""
	return def.WithObjectConstructor(fn)
}

func (s *moduleSchema) generatedCode(ctx context.Context, _ *core.Query, args struct {
	Code core.DirectoryID
}) (*core.GeneratedCode, error) {
	dir, err := args.Code.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return core.NewGeneratedCode(dir), nil
}

func (s *moduleSchema) generatedCodeWithVCSGeneratedPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

func (s *moduleSchema) module(ctx context.Context, query *core.Query, _ struct{}) (*core.Module, error) {
	return query.NewModule(), nil
}

func (s *moduleSchema) function(ctx context.Context, _ *core.Query, args struct {
	Name       string
	ReturnType core.TypeDefID
}) (*core.Function, error) {
	returnType, err := args.ReturnType.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	return core.NewFunction(args.Name, returnType.Self), nil
}

func (s *moduleSchema) functionWithDescription(ctx context.Context, fn *core.Function, args struct {
	Description string
}) (*core.Function, error) {
	return fn.WithDescription(args.Description), nil
}

func (s *moduleSchema) functionWithArg(ctx context.Context, fn *core.Function, args struct {
	Name         string
	TypeDef      core.TypeDefID
	Description  string    `default:""`
	DefaultValue core.JSON `default:""`
}) (*core.Function, error) {
	argType, err := args.TypeDef.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	return fn.WithArg(args.Name, argType.Self, args.Description, args.DefaultValue), nil
}

func (s *moduleSchema) moduleInitialize(
	ctx context.Context,
	inst dagql.Instance[*core.Module],
	args struct{},
) (*core.Module, error) {
	mod, err := s.updateCodegenAndRuntime(ctx, inst.Self)
	if err != nil {
		return nil, fmt.Errorf("failed to run sdk for module initialization: %w", err)
	}
	mod, err = mod.Initialize(ctx, inst, dagql.CurrentID(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize module: %w", err)
	}
	return mod, nil
}

type directoryAsModuleArgs struct {
	SourceSubpath string `default:"/"`
}

func (s *moduleSchema) directoryAsModule(ctx context.Context, sourceDir dagql.Instance[*core.Directory], args directoryAsModuleArgs) (*core.Module, error) {
	var inst dagql.Instance[*core.Module]
	err := s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(args.SourceSubpath)},
				{Name: "rootDirectory", Value: dagql.Opt(dagql.NewID[*core.Directory](sourceDir.ID()))},
			},
		},
		dagql.Selector{
			Field: "asModule",
		},
		dagql.Selector{
			Field: "initialize",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from directory: %w", err)
	}
	return inst.Self, nil
}

func (s *moduleSchema) currentModule(ctx context.Context, self *core.Query, _ struct{}) (inst dagql.Instance[*core.Module], err error) {
	id, err := self.CurrentModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current module: %w", err)
	}
	mod, err := id.Load(ctx, s.dag)
	if err != nil {
		return inst, fmt.Errorf("failed to load current module: %w", err)
	}

	return mod, nil
}

func (s *moduleSchema) currentFunctionCall(ctx context.Context, self *core.Query, _ struct{}) (*core.FunctionCall, error) {
	return self.CurrentFunctionCall(ctx)
}

func (s *moduleSchema) moduleServe(ctx context.Context, modMeta dagql.Instance[*core.Module], _ struct{}) (dagql.Nullable[core.Void], error) {
	return dagql.Null[core.Void](), modMeta.Self.Query.ServeModuleToMainClient(ctx, modMeta)
}

func (s *moduleSchema) currentTypeDefs(ctx context.Context, self *core.Query, _ struct{}) ([]*core.TypeDef, error) {
	deps, err := self.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	return deps.TypeDefs(ctx)
}

func (s *moduleSchema) functionCallReturnValue(ctx context.Context, fnCall *core.FunctionCall, args struct {
	Value core.JSON
}) (dagql.Nullable[core.Void], error) {
	// TODO: error out if caller is not coming from a module
	return dagql.Null[core.Void](), fnCall.ReturnValue(ctx, args.Value)
}

func (s *moduleSchema) moduleWithDescription(ctx context.Context, modMeta *core.Module, args struct {
	Description string
}) (*core.Module, error) {
	return modMeta.WithDescription(args.Description), nil
}

func (s *moduleSchema) moduleWithObject(ctx context.Context, modMeta *core.Module, args struct {
	Object core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Object.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return modMeta.WithObject(ctx, def.Self)
}

func (s *moduleSchema) moduleWithInterface(ctx context.Context, modMeta *core.Module, args struct {
	Iface core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Iface.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return modMeta.WithInterface(ctx, def.Self)
}

func (s *moduleSchema) moduleWithSource(ctx context.Context, self dagql.Instance[*core.Module], args struct {
	Source core.ModuleSourceID
}) (*core.Module, error) {
	src, err := args.Source.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode module source: %w", err)
	}

	src, rootDir, sourceSubpath, err := s.normalizeSourceForModule(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize module source: %w", err)
	}

	var modsCfg modules.ModulesConfig
	configFile, err := rootDir.Self.File(ctx, filepath.Join("/", modules.Filename))
	if err == nil {
		configBytes, err := configFile.Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read module config file: %w", err)
		}

		if err := json.Unmarshal(configBytes, &modsCfg); err != nil {
			return nil, fmt.Errorf("failed to decode module config: %w", err)
		}
		if err := modsCfg.Validate(); err != nil {
			return nil, fmt.Errorf("invalid module config: %w", err)
		}
	}
	relSubpath, err := filepath.Rel("/", sourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to get module source path relative to config: %w", err)
	}

	modCfg, foundModCfg := modsCfg.ModuleConfigByPath(relSubpath)
	if !foundModCfg {
		// no config for this module found, need to initialize a new one
		modCfg = &modules.ModuleConfig{
			Source: relSubpath,
		}
	}

	mod := self
	mod.Self = mod.Self.Clone()
	mod.Self.Source = src
	mod.Self.Source.Self = mod.Self.Source.Self.Clone()
	mod.Self.NameField = modCfg.Name
	mod.Self.SDKConfig = modCfg.SDK
	mod.Self.GeneratedSourceDirectory = rootDir
	mod.Self.GeneratedSourceDirectory.Self = rootDir.Self.Clone()
	mod.Self.GeneratedSourceSubpath = sourceSubpath
	mod.Self.DirectoryIncludeConfig = modCfg.Include
	mod.Self.DirectoryExcludeConfig = modCfg.Exclude

	// honor the include/exclude values for the directory (even if it's too late to actually use them
	// for loading performance optimizations)
	if len(modCfg.Include) > 0 || len(modCfg.Exclude) > 0 {
		mod.Self.GeneratedSourceDirectory.Self, err = mod.Self.GeneratedSourceDirectory.Self.WithDirectory(
			ctx,
			"/",
			mod.Self.GeneratedSourceDirectory.Self,
			core.CopyFilter{
				Include: modCfg.Include,
				Exclude: modCfg.Exclude,
			},
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to apply include/exclude to config directory: %w", err)
		}
	}

	depSrcs := make([]dagql.Instance[*core.ModuleSource], len(modCfg.Dependencies))
	var eg errgroup.Group
	for i, depCfg := range modCfg.Dependencies {
		i, depCfg := i, depCfg
		eg.Go(func() error {
			return s.dag.Select(ctx, s.dag.Root(), &depSrcs[i],
				dagql.Selector{
					Field: "moduleSource",
					Args: []dagql.NamedInput{
						{Name: "refString", Value: dagql.String(depCfg.Ref)},
						{Name: "rootDirectory", Value: dagql.Opt(dagql.NewID[*core.Directory](rootDir.ID()))},
					},
				},
			)
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to load dependency refs: %w", err)
	}
	mod.Self, err = mod.Self.WithDependencies(ctx, s.dag, depSrcs)
	if err != nil {
		return nil, fmt.Errorf("failed to set module dependencies config: %w", err)
	}

	return mod.Self, nil
}

func (s *moduleSchema) moduleWithName(ctx context.Context, mod *core.Module, args struct {
	Name string
}) (*core.Module, error) {
	mod, err := mod.WithName(ctx, args.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to set module name: %w", err)
	}

	// update dagger.json
	mod, err = s.updateModuleConfig(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}
	return mod, nil
}

func (s *moduleSchema) moduleWithSDK(ctx context.Context, mod *core.Module, args struct {
	SDK string
}) (*core.Module, error) {
	mod, err := mod.WithSDK(ctx, args.SDK)
	if err != nil {
		return nil, fmt.Errorf("failed to set module sdk: %w", err)
	}

	// update dagger.json
	mod, err = s.updateModuleConfig(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	return mod, nil
}

func (s *moduleSchema) moduleWithDependencies(ctx context.Context, inst dagql.Instance[*core.Module], args struct {
	Dependencies []core.ModuleSourceID
}) (*core.Module, error) {
	deps, err := collectIDInstances(ctx, s.dag, args.Dependencies)
	if err != nil {
		return nil, err
	}

	mod, err := inst.Self.WithDependencies(ctx, s.dag, deps)
	if err != nil {
		return nil, fmt.Errorf("failed to set module dependencies config: %w", err)
	}

	// update dagger.json
	mod, err = s.updateModuleConfig(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	return mod, nil
}

func (s *moduleSchema) moduleGeneratedSourceDirectory(
	ctx context.Context,
	mod *core.Module,
	args struct{},
) (*core.Directory, error) {
	// update dagger.json in case there were changes
	mod, err := s.updateModuleConfig(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	// run sdk codegen
	mod, err = s.updateCodegenAndRuntime(ctx, mod)
	if err != nil {
		return nil, fmt.Errorf("failed to update with sdk codegen and runtime: %w", err)
	}
	return mod.GeneratedSourceDirectory.Self, nil
}

func (s *moduleSchema) updateModuleConfig(ctx context.Context, mod *core.Module) (*core.Module, error) {
	cfgPath := filepath.Join("/", modules.Filename)

	var modsCfg modules.ModulesConfig
	modsCfgPerm := fs.FileMode(0644)
	cfgFile, err := mod.GeneratedSourceDirectory.Self.File(ctx, cfgPath)
	// err is nil if the file exists already, in which case we should update it
	if err == nil {
		cfgBytes, err := cfgFile.Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get module config file contents: %w", err)
		}
		if err := json.Unmarshal(cfgBytes, &modsCfg); err != nil {
			return nil, fmt.Errorf("failed to decode module config: %w", err)
		}

		cfgFileStat, err := cfgFile.Stat(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to stat module config file: %w", err)
		}
		modsCfgPerm = fs.FileMode(cfgFileStat.Mode & 0777)
	}

	sourceSubpath := mod.GeneratedSourceSubpath
	modSrcRelPath, err := filepath.Rel("/", sourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to get module source path relative to config: %w", err)
	}

	modCfg := &modules.ModuleConfig{
		Name:         mod.NameField,
		Source:       modSrcRelPath,
		SDK:          mod.SDKConfig,
		Dependencies: make([]*modules.ModuleConfigDependency, len(mod.DependencyConfig)),
		Include:      mod.DirectoryIncludeConfig,
		Exclude:      mod.DirectoryExcludeConfig,
	}
	for i, dep := range mod.DependencyConfig {
		refStr, err := dep.Source.Self.RefString()
		if err != nil {
			return nil, fmt.Errorf("failed to get ref string for dependency: %w", err)
		}
		modCfg.Dependencies[i] = &modules.ModuleConfigDependency{
			Ref: refStr,
		}
	}

	var found bool
	for i, maybeModCfg := range modsCfg.Modules {
		if maybeModCfg.Source == modCfg.Source {
			modsCfg.Modules[i] = modCfg
			found = true
			break
		}
	}
	if !found {
		modsCfg.Modules = append(modsCfg.Modules, modCfg)
	}

	if err := modsCfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid module config: %w", err)
	}

	updatedCfgBytes, err := json.MarshalIndent(modsCfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode module config: %w", err)
	}
	updatedCfgBytes = append(updatedCfgBytes, '\n')

	err = s.dag.Select(ctx, mod.GeneratedSourceDirectory, &mod.GeneratedSourceDirectory,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(cfgPath)},
				{Name: "contents", Value: dagql.String(updatedCfgBytes)},
				{Name: "permissions", Value: dagql.Int(modsCfgPerm)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config directory config file: %w", err)
	}

	return mod, nil
}

func (s *moduleSchema) updateCodegenAndRuntime(ctx context.Context, mod *core.Module) (*core.Module, error) {
	if mod.NameField == "" || mod.SDKConfig == "" {
		return nil, fmt.Errorf("module cannot be generated without both name and sdk set")
	}

	// run codegen + get the runtime container
	sdk, err := s.sdkForModule(ctx, mod.Query, mod.SDKConfig, mod.GeneratedSourceDirectory)
	if err != nil {
		return nil, fmt.Errorf("failed to get module sdk: %w", err)
	}

	sourceSubpath := mod.GeneratedSourceSubpath

	mod.Runtime, err = sdk.Runtime(ctx, mod, mod.GeneratedSourceDirectory, sourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to get module runtime: %w", err)
	}

	generatedCode, err := sdk.Codegen(ctx, mod, mod.GeneratedSourceDirectory, sourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to generate code: %w", err)
	}

	err = s.dag.Select(ctx, mod.GeneratedSourceDirectory, &mod.GeneratedSourceDirectory,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "directory", Value: dagql.NewID[*core.Directory](generatedCode.Code.ID())},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to overlay generated code onto source directory: %w", err)
	}

	// update .gitattributes
	gitAttrsPath := filepath.Join(sourceSubpath, ".gitattributes")
	var gitAttrsContents []byte
	gitAttrsFile, err := mod.GeneratedSourceDirectory.Self.File(ctx, gitAttrsPath)
	if err == nil {
		gitAttrsContents, err = gitAttrsFile.Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get git attributes file contents: %w", err)
		}
		if !bytes.HasSuffix(gitAttrsContents, []byte("\n")) {
			gitAttrsContents = append(gitAttrsContents, []byte("\n")...)
		}
	}
	for _, fileName := range generatedCode.VCSGeneratedPaths {
		if bytes.Contains(gitAttrsContents, []byte(fileName)) {
			// already has some config for the file
			continue
		}
		gitAttrsContents = append(gitAttrsContents,
			[]byte(fmt.Sprintf("/%s linguist-generated=true\n", fileName))...,
		)
	}

	err = s.dag.Select(ctx, mod.GeneratedSourceDirectory, &mod.GeneratedSourceDirectory,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(gitAttrsPath)},
				{Name: "contents", Value: dagql.String(gitAttrsContents)},
				{Name: "permissions", Value: dagql.Int(0600)},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add git attributes file: %w", err)
	}

	return mod, nil
}

type moduleSourceArgs struct {
	// avoiding name "ref" due to that being a reserved word in some SDKs (e.g. Rust)
	RefString string

	RootDirectory dagql.Optional[core.DirectoryID]
	Stable        bool `default:"false"`
}

func (s *moduleSchema) moduleSource(ctx context.Context, query *core.Query, args moduleSourceArgs) (*core.ModuleSource, error) {
	modPath, modVersion, hasVersion := strings.Cut(args.RefString, "@")

	isGitHub := strings.HasPrefix(modPath, "github.com/")

	if !hasVersion && isGitHub && args.Stable {
		return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
	}

	src := &core.ModuleSource{}
	if !hasVersion && !isGitHub {
		// assume local path
		// NB(vito): HTTP URLs should be supported by taking a sha256 digest as the
		// version. so it should be safe to assume no version = local path. as a
		// rule, if it's local we don't need to version it; if it's remote, we do.
		src.Kind = core.ModuleSourceKindLocal

		if args.RootDirectory.Valid {
			parentDir, err := args.RootDirectory.Value.Load(ctx, s.dag)
			if err != nil {
				return nil, fmt.Errorf("failed to load parent directory: %w", err)
			}
			src.RootDirectory = parentDir

			if !filepath.IsAbs(modPath) && !filepath.IsLocal(modPath) {
				return nil, fmt.Errorf("local module source subpath points out of root: %q", modPath)
			}
			modPath = filepath.Join("/", modPath)
		}

		src.AsLocalSource = dagql.NonNull(&core.LocalModuleSource{
			Subpath: modPath,
		})
	} else {
		if !isGitHub {
			return nil, fmt.Errorf("for now, only github.com/ paths are supported: %q", args.RefString)
		}
		src.Kind = core.ModuleSourceKindGit

		src.AsGitSource = dagql.NonNull(&core.GitModuleSource{})

		segments := strings.SplitN(modPath, "/", 4)
		if len(segments) < 3 {
			return nil, fmt.Errorf("invalid github.com path: %s", modPath)
		}

		src.AsGitSource.Value.URLParent = segments[0] + "/" + segments[1] + "/" + segments[2]

		cloneURL := src.AsGitSource.Value.CloneURL()

		if !hasVersion {
			if args.Stable {
				return nil, fmt.Errorf("no version provided for stable remote ref: %s", args.RefString)
			}

			var err error
			modVersion, err = defaultBranch(ctx, cloneURL)
			if err != nil {
				return nil, fmt.Errorf("determine default branch: %w", err)
			}
		}

		var gitRef dagql.Instance[*core.GitRef]
		err := s.dag.Select(ctx, s.dag.Root(), &gitRef,
			dagql.Selector{
				Field: "git",
				Args: []dagql.NamedInput{
					{Name: "url", Value: dagql.String(cloneURL)},
				},
			},
			dagql.Selector{
				Field: "commit",
				Args: []dagql.NamedInput{
					{Name: "id", Value: dagql.String(modVersion)},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve git src: %w", err)
		}
		gitCommit, err := gitRef.Self.Commit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve git src to commit: %w", err)
		}

		src.AsGitSource.Value.Version = gitCommit // TODO preserve semver here
		src.AsGitSource.Value.Commit = gitCommit  // but tell the truth here

		if len(segments) == 4 {
			src.AsGitSource.Value.Subpath = segments[3]
		} else {
			src.AsGitSource.Value.Subpath = "/"
		}
		subPath := src.AsGitSource.Value.Subpath
		if !filepath.IsAbs(subPath) && !filepath.IsLocal(subPath) {
			return nil, fmt.Errorf("git module source subpath points out of root: %q", subPath)
		}
		src.AsGitSource.Value.Subpath = filepath.Join("/", src.AsGitSource.Value.Subpath)

		err = s.dag.Select(ctx, gitRef, &src.RootDirectory,
			dagql.Selector{Field: "tree"},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load git dir: %w", err)
		}
	}

	return src, nil
}

func (s *moduleSchema) moduleSourceSubpath(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.Subpath()
}

func (s *moduleSchema) moduleSourceAsString(ctx context.Context, src *core.ModuleSource, args struct{}) (string, error) {
	return src.RefString()
}

func (s *moduleSchema) moduleSourceResolveDependency(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct {
		Dep core.ModuleSourceID
	},
) (*core.ModuleSource, error) {
	depSrc, err := args.Dep.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode module source: %w", err)
	}

	if depSrc.Self.Kind == core.ModuleSourceKindGit {
		// git deps stand on their own, no special handling needed
		return depSrc.Self, nil
	}

	// This dep is a local path relative to a src, need to find the src's root
	// and return a source that points to the full path to this dep

	sourceSubpath, err := src.Self.Subpath()
	if err != nil {
		return nil, fmt.Errorf("failed to get module source path: %w", err)
	}
	rootPath, ok, err := findUpConfigDir(ctx, src.Self.RootDirectory.Self, sourceSubpath)
	if err != nil {
		return nil, fmt.Errorf("error while finding config dir: %w", err)
	}
	if !ok {
		// src is not initialized w/ a dagger.json yet, can't really make a dep relative to nothing
		return nil, fmt.Errorf("cannot make a dependency relative to an uninitialized module source")
	}

	newDepSrc := src
	newDepSrc.Self = newDepSrc.Self.Clone()

	depSourceSubpath, err := depSrc.Self.Subpath()
	if err != nil {
		return nil, fmt.Errorf("failed to get module source path: %w", err)
	}
	if escapes, err := escapesParentDir(rootPath, depSourceSubpath); err != nil {
		return nil, fmt.Errorf("failed to check if module source path escapes root: %w", err)
	} else if escapes {
		return nil, fmt.Errorf("module dep source path escapes root: %q", depSourceSubpath)
	}
	fullDepSourcePath := filepath.Join(rootPath, depSourceSubpath)

	switch src.Self.Kind {
	case core.ModuleSourceKindGit:
		newDepSrc.Self.AsGitSource.Value.Subpath = fullDepSourcePath

		// preserve the git metadata by just constructing a modified git source ref string
		// and using that to load the dep
		newDepRefStr, err := newDepSrc.Self.RefString()
		if err != nil {
			return nil, fmt.Errorf("failed to get module source ref string: %w", err)
		}

		err = s.dag.Select(ctx, s.dag.Root(), &newDepSrc,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(newDepRefStr)},
					{Name: "stable", Value: dagql.Boolean(true)},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load git dep: %w", err)
		}
		return newDepSrc.Self, nil

	case core.ModuleSourceKindLocal:
		newDepSrc.Self.AsLocalSource.Value.Subpath = fullDepSourcePath

		err = s.dag.Select(ctx, s.dag.Root(), &newDepSrc,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(fullDepSourcePath)},
					{Name: "rootDirectory", Value: dagql.Opt(dagql.NewID[*core.Directory](src.Self.RootDirectory.ID()))},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load git dep: %w", err)
		}
		return newDepSrc.Self, nil

	default:
		return nil, fmt.Errorf("unsupported module source kind: %q", src.Self.Kind)
	}
}

func (s *moduleSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
	args struct{},
) (*core.Module, error) {
	if src.Self.RootDirectory.Self == nil {
		return nil, fmt.Errorf("cannot load module from module source with no root directory")
	}

	var mod dagql.Instance[*core.Module]
	err := s.dag.Select(ctx, s.dag.Root(), &mod,
		dagql.Selector{
			Field: "module",
		},
		dagql.Selector{
			Field: "withSource",
			Args: []dagql.NamedInput{
				{Name: "source", Value: dagql.NewID[*core.ModuleSource](src.ID())},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load local src module: %w", err)
	}
	return mod.Self, nil
}

func (s *moduleSchema) gitModuleSourceCloneURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.CloneURL(), nil
}

func (s *moduleSchema) gitModuleSourceHTMLURL(
	ctx context.Context,
	ref *core.GitModuleSource,
	args struct{},
) (string, error) {
	return ref.HTMLURL(), nil
}

/*
Convert the given module source into a normalized form that is consistent in ID and source dir/subpath, with
the directory containing dagger.json always present at the root of the returned Directory.

This is needed for a few different cases:
 1. If a git module source contains dagger.json at a subdirectory of the repo, we need to "re-root" it so
 2. Local modules sources can have many different IDs for equivalent sources due to the fact that the client
    can load them from arbitrary parent dirs from a filesystem.
*/
func (s *moduleSchema) normalizeSourceForModule(
	ctx context.Context,
	src dagql.Instance[*core.ModuleSource],
) (
	newSrc dagql.Instance[*core.ModuleSource],
	newRootDir dagql.Instance[*core.Directory],
	newSourceSubpath string,
	rerr error,
) {
	sourceSubpath, err := src.Self.Subpath()
	if err != nil {
		return newSrc, newRootDir, "", fmt.Errorf("failed to get module source subpath: %w", err)
	}
	sourceSubpath = filepath.Join("/", sourceSubpath)

	rootDirPath, foundModsCfg, err := findUpConfigDir(ctx, src.Self.RootDirectory.Self, sourceSubpath)
	if err != nil {
		return newSrc, newRootDir, "", fmt.Errorf("error while finding config dir: %w", err)
	}
	if !foundModsCfg {
		// No dagger.json found (okay if this is an uninitialized module source dir). Default to the root.
		rootDirPath = "/"
	}

	// Reposition the root of the dir to the subdir containing the actual config
	srcSubdirRelPath, err := filepath.Rel(rootDirPath, sourceSubpath)
	if err != nil {
		return newSrc, newRootDir, "", fmt.Errorf("failed to get module source path relative to config: %w", err)
	}

	switch src.Self.Kind {
	case core.ModuleSourceKindGit:
		if rootDirPath == "/" {
			// No need to re-root
			return src, src.Self.RootDirectory, sourceSubpath, nil
		}

		// Git sources are inherently normalized in terms of ID, but the root directory still needs to be
		// re-rooted for the module
		err = s.dag.Select(ctx, src.Self.RootDirectory, &newRootDir, dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(rootDirPath)},
			},
		})
		if err != nil {
			return newSrc, newRootDir, "", fmt.Errorf("failed to reroot config directory: %w", err)
		}
		return src, newRootDir, filepath.Join("/", srcSubdirRelPath), nil

	case core.ModuleSourceKindLocal:
		/*
			Local sources are the tricky one since the root dir is just some arbitrary directory (usually a local dir
			loaded from a client's host filesystem).

			The only way to consistently normalize the ID is to use a blob source, making the source id'd by its
			content hash. In the case where this is an already correctly rooted source from a loaded local dir,
			this should be mostly a cheap no-op since the blob will already be cached.
		*/
		rootDir, err := src.Self.RootDirectory.Self.Directory(ctx, rootDirPath)
		if err != nil {
			return newSrc, newRootDir, "", fmt.Errorf("failed to get root subdir dir: %w", err)
		}
		newRootDir, err = rootDir.AsBlob(ctx, s.dag)
		if err != nil {
			return newSrc, newRootDir, "", fmt.Errorf("failed to get root subdir dir blob: %w", err)
		}

		err = s.dag.Select(ctx, s.dag.Root(), &newSrc,
			dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(srcSubdirRelPath)},
					{Name: "rootDirectory", Value: dagql.Opt(dagql.NewID[*core.Directory](newRootDir.ID()))},
				},
			},
		)
		if err != nil {
			return newSrc, newRootDir, "", fmt.Errorf("failed to load normalized module source: %w", err)
		}
		return newSrc, newRootDir, filepath.Join("/", srcSubdirRelPath), nil

	default:
		return newSrc, newRootDir, "", fmt.Errorf("unknown module src kind: %q", src.Self.Kind)
	}
}

func findUpConfigDir(
	ctx context.Context,
	dir *core.Directory,
	curDirPath string,
) (string, bool, error) {
	curDirPath = filepath.Clean(curDirPath)
	if !filepath.IsAbs(curDirPath) {
		return "", false, fmt.Errorf("path is not absolute: %s", curDirPath)
	}

	configPath := filepath.Join(curDirPath, modules.Filename)
	_, err := dir.File(ctx, configPath)
	if err == nil {
		return curDirPath, true, nil
	}

	// didn't exist, try parent unless we've hit "/" or a git repo checkout root
	if curDirPath == "/" {
		return curDirPath, false, nil
	}
	_, err = dir.Directory(ctx, filepath.Join(curDirPath, ".git"))
	if err == nil {
		return curDirPath, false, nil
	}

	parentDirPath := filepath.Dir(curDirPath)
	return findUpConfigDir(ctx, dir, parentDirPath)
}

// checks whether childRelPath goes above parentAbsPath, handling corner cases around
// the fact that e.g. /../../.. is still /, etc.
func escapesParentDir(parentAbsPath string, childRelPath string) (bool, error) {
	if !filepath.IsAbs(parentAbsPath) {
		return false, fmt.Errorf("parent path is not absolute: %s", parentAbsPath)
	}
	if filepath.IsAbs(childRelPath) {
		return false, fmt.Errorf("child path is not relative: %s", childRelPath)
	}

	parentRelPath, err := filepath.Rel("/", parentAbsPath)
	if err != nil {
		return false, fmt.Errorf("failed to get parent path relative to root: %w", err)
	}
	joinedPath := filepath.Join(parentRelPath, childRelPath)
	return !filepath.IsLocal(joinedPath), nil
}
