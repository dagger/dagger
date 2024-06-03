package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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
			ArgDoc("stable", `If true, enforce that the source is a stable version for source kinds that support versioning.`),

		dagql.Func("moduleDependency", s.moduleDependency).
			Doc(`Create a new module dependency configuration from a module source and name`).
			ArgDoc("source", `The source of the dependency`).
			ArgDoc("name", `If set, the name to use for the dependency. Otherwise, once installed to a parent module, the name of the dependency module will be used by default.`),

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
			ArgDoc("sourceRootPath",
				`An optional subpath of the directory which contains the module's configuration file.`,
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
		dagql.Func("contextDirectory", s.moduleSourceContextDirectory).
			Doc(`The directory containing everything needed to load load and use the module.`),

		dagql.Func("withContextDirectory", s.moduleSourceWithContextDirectory).
			Doc(`Update the module source with a new context directory. Only valid for local sources.`).
			ArgDoc("dir", `The directory to set as the context directory.`),

		dagql.Func("directory", s.moduleSourceDirectory).
			Doc(`The directory containing the module configuration and source code (source code may be in a subdir).`).
			ArgDoc(`path`, `The path from the source directory to select.`),

		dagql.Func("sourceRootSubpath", s.moduleSourceRootSubpath).
			Doc(`The path relative to context of the root of the module source, which contains dagger.json. It also contains the module implementation source code, but that may or may not being a subdir of this root.`),

		dagql.Func("sourceSubpath", s.moduleSourceSubpath).
			Doc(`The path relative to context of the module implementation source code.`),

		dagql.Func("withSourceSubpath", s.moduleSourceWithSourceSubpath).
			Doc(`Update the module source with a new source subpath.`).
			ArgDoc("path", `The path to set as the source subpath.`),

		dagql.Func("moduleName", s.moduleSourceModuleName).
			Doc(`If set, the name of the module this source references, including any overrides at runtime by callers.`),

		dagql.Func("moduleOriginalName", s.moduleSourceModuleOriginalName).
			Doc(`The original name of the module this source references, as defined in the module configuration.`),

		dagql.Func("withName", s.moduleSourceWithName).
			Doc(`Update the module source with a new name.`).
			ArgDoc("name", `The name to set.`),

		dagql.NodeFunc("dependencies", s.moduleSourceDependencies).
			Doc(`The dependencies of the module source. Includes dependencies from the configuration and any extras from withDependencies calls.`),

		dagql.Func("withDependencies", s.moduleSourceWithDependencies).
			Doc(`Append the provided dependencies to the module source's dependency list.`).
			ArgDoc("dependencies", `The dependencies to append.`),

		dagql.Func("withSDK", s.moduleSourceWithSDK).
			Doc(`Update the module source with a new SDK.`).
			ArgDoc("sdk", `The SDK to set.`),

		dagql.Func("configExists", s.moduleSourceConfigExists).
			Doc(`Returns whether the module source has a configuration file.`),

		dagql.Func("resolveDependency", s.moduleSourceResolveDependency).
			Doc(`Resolve the provided module source arg as a dependency relative to this module source.`).
			ArgDoc("dep", `The dependency module source to resolve.`),

		dagql.Func("asString", s.moduleSourceAsString).
			Doc(`A human readable ref string representation of this module source.`),

		dagql.NodeFunc("asModule", s.moduleSourceAsModule).
			Doc(`Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation`),

		dagql.Func("resolveFromCaller", s.moduleSourceResolveFromCaller).
			Impure(`Loads live caller-specific data from their filesystem.`).
			Doc(`Load the source from its path on the caller's filesystem, including only needed+configured files and directories. Only valid for local sources.`),

		dagql.Func("resolveContextPathFromCaller", s.moduleSourceResolveContextPathFromCaller).
			Impure(`Queries live caller-specific data from their filesystem.`).
			Doc(`The path to the module source's context directory on the caller's filesystem. Only valid for local sources.`),

		dagql.Func("resolveDirectoryFromCaller", s.moduleSourceResolveDirectoryFromCaller).
			Impure(`Queries live caller-specific data from their filesystem.`).
			ArgDoc("path", `The path on the caller's filesystem to load.`).
			ArgDoc("viewName", `If set, the name of the view to apply to the path.`).
			Doc(`Load a directory from the caller optionally with a given view applied.`),

		dagql.Func("views", s.moduleSourceViews).
			Doc(`The named views defined for this module source, which are sets of directory filters that can be applied to directory arguments provided to functions.`),

		dagql.Func("view", s.moduleSourceView).
			ArgDoc("name", `The name of the view to retrieve.`).
			Doc(`Retrieve a named view defined for this module source.`),

		dagql.Func("withView", s.moduleSourceWithView).
			ArgDoc("name", `The name of the view to set.`).
			ArgDoc("patterns", `The patterns to set as the view filters.`).
			Doc(`Update the module source with a new named view.`),
	}.Install(s.dag)

	dagql.Fields[*core.ModuleSourceView]{
		dagql.Func("name", s.moduleSourceViewName).
			Doc(`The name of the view`),
		dagql.Func("patterns", s.moduleSourceViewPatterns).
			Doc(`The patterns of the view used to filter paths`),
	}.Install(s.dag)

	dagql.Fields[*core.LocalModuleSource]{}.Install(s.dag)

	dagql.Fields[*core.GitModuleSource]{
		dagql.Func("htmlURL", s.gitModuleSourceHTMLURL).
			Doc(`The URL to the source's git repo in a web browser`),
	}.Install(s.dag)

	dagql.Fields[*core.ModuleDependency]{}.Install(s.dag)

	dagql.Fields[*core.Module]{
		dagql.Func("withSource", s.moduleWithSource).
			Doc(`Retrieves the module with basic configuration loaded if present.`).
			ArgDoc("source", `The module source to initialize from.`),

		dagql.Func("generatedContextDiff", s.moduleGeneratedContextDiff).
			Doc(`The generated files and directories made on top of the module source's context directory.`),

		dagql.NodeFunc("initialize", s.moduleInitialize).
			Doc(`Retrieves the module with the objects loaded via its SDK.`),

		dagql.Func("withDescription", s.moduleWithDescription).
			Doc(`Retrieves the module with the given description`).
			ArgDoc("description", `The description to set`),

		dagql.Func("withObject", s.moduleWithObject).
			Doc(`This module plus the given Object type and associated functions.`),

		dagql.Func("withInterface", s.moduleWithInterface).
			Doc(`This module plus the given Interface type and associated functions`),

		dagql.Func("withEnum", s.moduleWithEnum).
			Doc(`This module plus the given Enum type and associated values`),

		dagql.NodeFunc("serve", s.moduleServe).
			Impure(`Mutates the calling session's global schema.`).
			Doc(`Serve a module's API in the current session.`,
				`Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.`),
	}.Install(s.dag)

	dagql.Fields[*core.CurrentModule]{
		dagql.Func("name", s.currentModuleName).
			Doc(`The name of the module being executed in`),

		dagql.Func("source", s.currentModuleSource).
			Doc(`The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).`),

		dagql.Func("workdir", s.currentModuleWorkdir).
			Impure(`Loads live caller-specific data from their filesystem.`).
			Doc(`Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.`).
			ArgDoc("path", `Location of the directory to access (e.g., ".").`).
			ArgDoc("exclude", `Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`).
			ArgDoc("include", `Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),

		dagql.Func("workdirFile", s.currentModuleWorkdirFile).
			Impure(`Loads live caller-specific data from their filesystem.`).
			Doc(`Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.`).
			ArgDoc("path", `Location of the file to retrieve (e.g., "README.md").`),
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

		dagql.Func("withScalar", s.typeDefWithScalar).
			Doc(`Returns a TypeDef of kind Scalar with the provided name.`),

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

		dagql.Func("withEnum", s.typeDefWithEnum).
			Doc(`Returns a TypeDef of kind Enum with the provided name.`,
				`Note that an enum's values may be omitted if the intent is only to refer to an enum.
				This is how functions are able to return their own, or any other circular reference.`).
			ArgDoc("name", `The name of the enum`).
			ArgDoc("description", `A doc string for the enum, if any`),

		dagql.Func("withEnumValue", s.typeDefWithEnumValue).
			Doc(`Adds a static value for an Enum TypeDef, failing if the type is not an enum.`).
			ArgDoc("value", `The name of the value in the enum`).
			ArgDoc("description", `A doc string for the value, if any`),
	}.Install(s.dag)

	dagql.Fields[*core.ObjectTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.InterfaceTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.InputTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.FieldTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.ListTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.ScalarTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.EnumTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.EnumValueTypeDef]{}.Install(s.dag)

	dagql.Fields[*core.GeneratedCode]{
		dagql.Func("withVCSGeneratedPaths", s.generatedCodeWithVCSGeneratedPaths).
			Doc(`Set the list of paths to mark generated in version control.`),
		dagql.Func("withVCSIgnoredPaths", s.generatedCodeWithVCSIgnoredPaths).
			Doc(`Set the list of paths to ignore in version control.`),
	}.Install(s.dag)
}

func (s *moduleSchema) typeDef(ctx context.Context, _ *core.Query, args struct{}) (*core.TypeDef, error) {
	return &core.TypeDef{}, nil
}

func (s *moduleSchema) typeDefWithOptional(ctx context.Context, def *core.TypeDef, args struct {
	Optional bool
},
) (*core.TypeDef, error) {
	return def.WithOptional(args.Optional), nil
}

func (s *moduleSchema) typeDefWithKind(ctx context.Context, def *core.TypeDef, args struct {
	Kind core.TypeDefKind
},
) (*core.TypeDef, error) {
	return def.WithKind(args.Kind), nil
}

func (s *moduleSchema) typeDefWithScalar(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
},
) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("scalar type def must have a name")
	}
	return def.WithScalar(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithListOf(ctx context.Context, def *core.TypeDef, args struct {
	ElementType core.TypeDefID
},
) (*core.TypeDef, error) {
	elemType, err := args.ElementType.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithListOf(elemType.Self), nil
}

func (s *moduleSchema) typeDefWithObject(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
},
) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("object type def must have a name")
	}
	return def.WithObject(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithInterface(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
},
) (*core.TypeDef, error) {
	return def.WithInterface(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string `default:""`
},
) (*core.TypeDef, error) {
	fieldType, err := args.TypeDef.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithObjectField(args.Name, fieldType.Self, args.Description)
}

func (s *moduleSchema) typeDefWithFunction(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
},
) (*core.TypeDef, error) {
	fn, err := args.Function.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return def.WithFunction(fn.Self)
}

func (s *moduleSchema) typeDefWithObjectConstructor(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
},
) (*core.TypeDef, error) {
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

func (s *moduleSchema) typeDefWithEnum(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
},
) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("enum type def must have a name")
	}

	return def.WithEnum(args.Name, args.Description), nil
}

func (s *moduleSchema) typeDefWithEnumValue(ctx context.Context, def *core.TypeDef, args struct {
	Value       string
	Description string `default:""`
},
) (*core.TypeDef, error) {
	if args.Value == "" {
		return nil, fmt.Errorf("enum value must have a name")
	}

	return def.WithEnumValue(args.Value, args.Description)
}

func (s *moduleSchema) generatedCode(ctx context.Context, _ *core.Query, args struct {
	Code core.DirectoryID
},
) (*core.GeneratedCode, error) {
	dir, err := args.Code.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return core.NewGeneratedCode(dir), nil
}

func (s *moduleSchema) generatedCodeWithVCSGeneratedPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
},
) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

func (s *moduleSchema) generatedCodeWithVCSIgnoredPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
},
) (*core.GeneratedCode, error) {
	return code.WithVCSIgnoredPaths(args.Paths), nil
}

func (s *moduleSchema) module(ctx context.Context, query *core.Query, _ struct{}) (*core.Module, error) {
	return query.NewModule(), nil
}

func (s *moduleSchema) function(ctx context.Context, _ *core.Query, args struct {
	Name       string
	ReturnType core.TypeDefID
},
) (*core.Function, error) {
	returnType, err := args.ReturnType.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	return core.NewFunction(args.Name, returnType.Self), nil
}

func (s *moduleSchema) functionWithDescription(ctx context.Context, fn *core.Function, args struct {
	Description string
},
) (*core.Function, error) {
	return fn.WithDescription(args.Description), nil
}

func (s *moduleSchema) functionWithArg(ctx context.Context, fn *core.Function, args struct {
	Name         string
	TypeDef      core.TypeDefID
	Description  string    `default:""`
	DefaultValue core.JSON `default:""`
},
) (*core.Function, error) {
	argType, err := args.TypeDef.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	return fn.WithArg(args.Name, argType.Self, args.Description, args.DefaultValue), nil
}

func (s *moduleSchema) moduleDependency(
	ctx context.Context,
	query *core.Query,
	args struct {
		Source core.ModuleSourceID
		Name   string `default:""`
	},
) (*core.ModuleDependency, error) {
	src, err := args.Source.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode dependency source: %w", err)
	}

	return &core.ModuleDependency{
		Source: src,
		Name:   args.Name,
	}, nil
}

func (s *moduleSchema) currentModule(
	ctx context.Context,
	self *core.Query,
	_ struct{},
) (*core.CurrentModule, error) {
	mod, err := self.CurrentModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	return &core.CurrentModule{Module: mod}, nil
}

func (s *moduleSchema) currentFunctionCall(ctx context.Context, self *core.Query, _ struct{}) (*core.FunctionCall, error) {
	return self.CurrentFunctionCall(ctx)
}

func (s *moduleSchema) moduleServe(ctx context.Context, modMeta dagql.Instance[*core.Module], _ struct{}) (dagql.Nullable[core.Void], error) {
	return dagql.Null[core.Void](), modMeta.Self.Query.ServeModule(ctx, modMeta.Self)
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
},
) (dagql.Nullable[core.Void], error) {
	// TODO: error out if caller is not coming from a module
	return dagql.Null[core.Void](), fnCall.ReturnValue(ctx, args.Value)
}

func (s *moduleSchema) moduleWithDescription(ctx context.Context, mod *core.Module, args struct {
	Description string
},
) (*core.Module, error) {
	return mod.WithDescription(args.Description), nil
}

func (s *moduleSchema) moduleWithObject(ctx context.Context, mod *core.Module, args struct {
	Object core.TypeDefID
},
) (_ *core.Module, rerr error) {
	def, err := args.Object.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return mod.WithObject(ctx, def.Self)
}

func (s *moduleSchema) moduleWithInterface(ctx context.Context, mod *core.Module, args struct {
	Iface core.TypeDefID
},
) (_ *core.Module, rerr error) {
	def, err := args.Iface.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return mod.WithInterface(ctx, def.Self)
}

func (s *moduleSchema) moduleWithEnum(ctx context.Context, mod *core.Module, args struct {
	Enum core.TypeDefID
},
) (_ *core.Module, rerr error) {
	def, err := args.Enum.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}

	return mod.WithEnum(ctx, def.Self)
}

func (s *moduleSchema) currentModuleName(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct{},
) (string, error) {
	return curMod.Module.Name(), nil
}

func (s *moduleSchema) currentModuleSource(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	srcSubpath, err := curMod.Module.Source.Self.SourceSubpathWithDefault(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get module source subpath: %w", err)
	}

	err = s.dag.Select(ctx, curMod.Module.GeneratedContextDirectory, &inst,
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(srcSubpath)},
			},
		},
	)
	return inst, err
}

func (s *moduleSchema) currentModuleWorkdir(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct {
		Path string
		core.CopyFilter
	},
) (inst dagql.Instance[*core.Directory], err error) {
	if !filepath.IsLocal(args.Path) {
		return inst, fmt.Errorf("workdir path %q escapes workdir", args.Path)
	}
	args.Path = filepath.Join(runtimeWorkdirPath, args.Path)

	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(args.Path)},
				{Name: "exclude", Value: asArrayInput(args.Exclude, dagql.NewString)},
				{Name: "include", Value: asArrayInput(args.Include, dagql.NewString)},
			},
		},
	)
	return inst, err
}

func (s *moduleSchema) currentModuleWorkdirFile(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct {
		Path string
	},
) (inst dagql.Instance[*core.File], err error) {
	if !filepath.IsLocal(args.Path) {
		return inst, fmt.Errorf("workdir path %q escapes workdir", args.Path)
	}
	args.Path = filepath.Join(runtimeWorkdirPath, args.Path)

	err = s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(args.Path)},
			},
		},
	)
	return inst, err
}

type directoryAsModuleArgs struct {
	SourceRootPath string `default:"."`
}

func (s *moduleSchema) directoryAsModule(ctx context.Context, contextDir dagql.Instance[*core.Directory], args directoryAsModuleArgs) (*core.Module, error) {
	var inst dagql.Instance[*core.Module]
	err := s.dag.Select(ctx, s.dag.Root(), &inst,
		dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(args.SourceRootPath)},
			},
		},
		dagql.Selector{
			Field: "withContextDirectory",
			Args: []dagql.NamedInput{
				{Name: "dir", Value: dagql.NewID[*core.Directory](contextDir.ID())},
			},
		},
		dagql.Selector{
			Field: "asModule",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create module from directory: %w", err)
	}
	return inst.Self, nil
}

// TODO: initialize probably doesn't need to exist anymore, can just try to init in withSource
// and, if error, return that error in future calls that rely on the module being initialized
func (s *moduleSchema) moduleInitialize(
	ctx context.Context,
	inst dagql.Instance[*core.Module],
	args struct{},
) (*core.Module, error) {
	if inst.Self.NameField == "" || inst.Self.SDKConfig == "" {
		return nil, fmt.Errorf("module name and SDK must be set")
	}
	mod, err := inst.Self.Initialize(ctx, inst.ID(), dagql.CurrentID(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize module: %w", err)
	}
	return mod, nil
}

func (s *moduleSchema) moduleWithSource(ctx context.Context, mod *core.Module, args struct {
	Source core.ModuleSourceID
},
) (*core.Module, error) {
	src, err := args.Source.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode module source: %w", err)
	}

	mod = mod.Clone()
	mod.Source = src
	mod.NameField, err = src.Self.ModuleName(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module name: %w", err)
	}
	mod.OriginalName, err = src.Self.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module original name: %w", err)
	}

	mod.SDKConfig, err = src.Self.SDK(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module SDK: %w", err)
	}

	if err := s.updateDeps(ctx, mod, src); err != nil {
		return nil, fmt.Errorf("failed to update module dependencies: %w", err)
	}
	if err := s.updateCodegenAndRuntime(ctx, mod, src); err != nil {
		return nil, fmt.Errorf("failed to update codegen and runtime: %w", err)
	}
	// update dagger.json last so SDKs can't intentionally or unintentionally
	// modify it during codegen in ways that would be hard to deal with
	if err := s.updateDaggerConfig(ctx, mod, src); err != nil {
		return nil, fmt.Errorf("failed to update dagger.json: %w", err)
	}

	return mod, nil
}

func (s *moduleSchema) moduleGeneratedContextDiff(
	ctx context.Context,
	mod *core.Module,
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	baseContext, err := mod.Source.Self.ContextDirectory()
	if err != nil {
		return inst, fmt.Errorf("failed to get base context directory: %w", err)
	}

	var diff dagql.Instance[*core.Directory]
	err = s.dag.Select(ctx, baseContext, &diff,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.Directory](mod.GeneratedContextDirectory.ID())},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to diff generated context: %w", err)
	}
	return diff, nil
}

func (s *moduleSchema) updateDeps(
	ctx context.Context,
	mod *core.Module,
	src dagql.Instance[*core.ModuleSource],
) error {
	var deps []dagql.Instance[*core.ModuleDependency]
	err := s.dag.Select(ctx, src, &deps, dagql.Selector{Field: "dependencies"})
	if err != nil {
		return fmt.Errorf("failed to load module dependencies: %w", err)
	}
	mod.DependencyConfig = make([]*core.ModuleDependency, len(deps))
	for i, dep := range deps {
		// verify that the dependency config actually exists
		_, cfgExists, err := dep.Self.Source.Self.ModuleConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to load module %q dependency %q config: %w", mod.NameField, dep.Self.Name, err)
		}
		if !cfgExists {
			// best effort for err message, ignore err
			sourceRootPath, _ := dep.Self.Source.Self.SourceRootSubpath()
			return fmt.Errorf("module %q dependency %q with source root path %q does not exist or does not have a configuration file", mod.NameField, dep.Self.Name, sourceRootPath)
		}
		mod.DependencyConfig[i] = dep.Self
	}

	mod.DependenciesField = make([]dagql.Instance[*core.Module], len(deps))
	var eg errgroup.Group
	for i, dep := range deps {
		i, dep := i, dep
		eg.Go(func() error {
			err := s.dag.Select(ctx, dep.Self.Source, &mod.DependenciesField[i],
				dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(dep.Self.Name)},
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
				return fmt.Errorf("failed to initialize dependency module: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to initialize dependency modules: %w", err)
	}

	mod.Deps = core.NewModDeps(src.Self.Query, src.Self.Query.DefaultDeps.Mods)
	for _, dep := range mod.DependenciesField {
		mod.Deps = mod.Deps.Append(dep.Self)
	}

	return nil
}

func (s *moduleSchema) updateCodegenAndRuntime(
	ctx context.Context,
	mod *core.Module,
	src dagql.Instance[*core.ModuleSource],
) error {
	if mod.NameField == "" || mod.SDKConfig == "" {
		// can't codegen yet
		return nil
	}

	baseContext, err := src.Self.ContextDirectory()
	if err != nil {
		return fmt.Errorf("failed to get base context directory: %w", err)
	}
	mod.GeneratedContextDirectory = baseContext

	sourceSubpath, err := src.Self.SourceSubpathWithDefault(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source root subpath: %w", err)
	}

	sdk, err := s.sdkForModule(ctx, src.Self.Query, mod.SDKConfig, src)
	if err != nil {
		return fmt.Errorf("failed to load sdk for module: %w", err)
	}

	generatedCode, err := sdk.Codegen(ctx, mod.Deps, src)
	if err != nil {
		return fmt.Errorf("failed to generate code: %w", err)
	}

	var diff dagql.Instance[*core.Directory]
	err = s.dag.Select(ctx, baseContext, &diff,
		dagql.Selector{
			Field: "diff",
			Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.Directory](generatedCode.Code.ID())},
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to diff generated code: %w", err)
	}

	err = s.dag.Select(ctx, mod.GeneratedContextDirectory, &mod.GeneratedContextDirectory,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "directory", Value: dagql.NewID[*core.Directory](diff.ID())},
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add codegen to module context directory: %w", err)
	}

	// update .gitattributes
	// (linter thinks this chunk of code is too similar to the below, but not clear abstraction is worth it)
	//nolint:dupl
	if len(generatedCode.VCSGeneratedPaths) > 0 {
		gitAttrsPath := filepath.Join(sourceSubpath, ".gitattributes")
		var gitAttrsContents []byte
		gitAttrsFile, err := baseContext.Self.File(ctx, gitAttrsPath)
		if err == nil {
			gitAttrsContents, err = gitAttrsFile.Contents(ctx)
			if err != nil {
				return fmt.Errorf("failed to get git attributes file contents: %w", err)
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
				[]byte(fmt.Sprintf("/%s linguist-generated\n", fileName))...,
			)
		}

		err = s.dag.Select(ctx, mod.GeneratedContextDirectory, &mod.GeneratedContextDirectory,
			dagql.Selector{
				Field: "withNewFile",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(gitAttrsPath)},
					{Name: "contents", Value: dagql.String(gitAttrsContents)},
					{Name: "permissions", Value: dagql.Int(0o600)},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("failed to add vcs generated file: %w", err)
		}
	}

	// update .gitignore
	automaticGitignoreSetting, err := src.Self.AutomaticGitignore(ctx)
	if err != nil {
		return fmt.Errorf("failed to get automatic gitignore setting: %w", err)
	}
	writeGitignore := true // default to true if not set
	if automaticGitignoreSetting != nil {
		writeGitignore = *automaticGitignoreSetting
	}
	// (linter thinks this chunk of code is too similar to the above, but not clear abstraction is worth it)
	//nolint:dupl
	if writeGitignore && len(generatedCode.VCSIgnoredPaths) > 0 {
		gitIgnorePath := filepath.Join(sourceSubpath, ".gitignore")
		var gitIgnoreContents []byte
		gitIgnoreFile, err := baseContext.Self.File(ctx, gitIgnorePath)
		if err == nil {
			gitIgnoreContents, err = gitIgnoreFile.Contents(ctx)
			if err != nil {
				return fmt.Errorf("failed to get .gitignore file contents: %w", err)
			}
			if !bytes.HasSuffix(gitIgnoreContents, []byte("\n")) {
				gitIgnoreContents = append(gitIgnoreContents, []byte("\n")...)
			}
		}
		for _, fileName := range generatedCode.VCSIgnoredPaths {
			if bytes.Contains(gitIgnoreContents, []byte(fileName)) {
				continue
			}
			gitIgnoreContents = append(gitIgnoreContents,
				[]byte(fmt.Sprintf("/%s\n", fileName))...,
			)
		}

		err = s.dag.Select(ctx, mod.GeneratedContextDirectory, &mod.GeneratedContextDirectory,
			dagql.Selector{
				Field: "withNewFile",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(gitIgnorePath)},
					{Name: "contents", Value: dagql.String(gitIgnoreContents)},
					{Name: "permissions", Value: dagql.Int(0o600)},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("failed to add vcs ignore file: %w", err)
		}
	}

	mod.Runtime, err = sdk.Runtime(ctx, mod.Deps, src)
	if err != nil {
		return fmt.Errorf("failed to get module runtime: %w", err)
	}

	return nil
}

func (s *moduleSchema) updateDaggerConfig(
	ctx context.Context,
	mod *core.Module,
	src dagql.Instance[*core.ModuleSource],
) error {
	modCfg, ok, err := src.Self.ModuleConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get module config: %w", err)
	}
	if !ok {
		modCfg = &modules.ModuleConfig{}
	}

	modCfg.Name = mod.OriginalName
	modCfg.SDK = mod.SDKConfig
	modCfg.EngineVersion = engine.Version

	sourceRootSubpath, err := src.Self.SourceRootSubpath()
	if err != nil {
		return fmt.Errorf("failed to get source root subpath: %w", err)
	}
	sourceSubpath, err := src.Self.SourceSubpathWithDefault(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source subpath: %w", err)
	}
	sourceRelSubpath, err := filepath.Rel(sourceRootSubpath, sourceSubpath)
	if err != nil {
		return fmt.Errorf("failed to get relative source subpath: %w", err)
	}
	if sourceRelSubpath != "." {
		modCfg.Source = sourceRelSubpath
	}

	views, err := src.Self.Views(ctx)
	if err != nil {
		return fmt.Errorf("failed to get views: %w", err)
	}
	modCfg.Views = nil
	for _, view := range views {
		if len(view.Patterns) == 0 {
			continue
		}
		modCfg.Views = append(modCfg.Views, view.ModuleConfigView)
	}

	modCfg.Dependencies = make([]*modules.ModuleConfigDependency, len(mod.DependencyConfig))
	for i, dep := range mod.DependencyConfig {
		var srcStr string
		switch dep.Source.Self.Kind {
		case core.ModuleSourceKindLocal:
			// make it relative to this module's source root
			depRootSubpath, err := dep.Source.Self.SourceRootSubpath()
			if err != nil {
				return fmt.Errorf("failed to get source root subpath: %w", err)
			}
			depRelPath, err := filepath.Rel(sourceRootSubpath, depRootSubpath)
			if err != nil {
				return fmt.Errorf("failed to get relative path to dep: %w", err)
			}
			srcStr = depRelPath

		case core.ModuleSourceKindGit:
			srcStr = dep.Source.Self.AsGitSource.Value.RefString()

		default:
			return fmt.Errorf("unsupported dependency source kind: %s", dep.Source.Self.Kind)
		}

		depName := dep.Name
		if dep.Name == "" {
			// fill in default dep names if missing with the name of the module
			depName = mod.DependenciesField[i].Self.Name()
		}

		modCfg.Dependencies[i] = &modules.ModuleConfigDependency{
			Name:   depName,
			Source: srcStr,
		}
	}

	rootSubpath, err := src.Self.SourceRootSubpath()
	if err != nil {
		return fmt.Errorf("failed to get source root subpath: %w", err)
	}

	modCfgPath := filepath.Join(rootSubpath, modules.Filename)
	updatedModCfgBytes, err := json.MarshalIndent(modCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode module config: %w", err)
	}
	updatedModCfgBytes = append(updatedModCfgBytes, '\n')

	if mod.GeneratedContextDirectory.Self == nil {
		// valid case for sdk-less modules (i.e. dep only), initialize as empty directory
		err = s.dag.Select(ctx, s.dag.Root(), &mod.GeneratedContextDirectory,
			dagql.Selector{Field: "directory"},
		)
		if err != nil {
			return fmt.Errorf("failed to initialize module context directory: %w", err)
		}
	}

	err = s.dag.Select(ctx, mod.GeneratedContextDirectory, &mod.GeneratedContextDirectory,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(modCfgPath)},
				{Name: "contents", Value: dagql.String(updatedModCfgBytes)},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to update module context directory config file: %w", err)
	}

	return nil
}
