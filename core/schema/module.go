package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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

		dagql.Func("function", s.function).
			Doc(`Creates a function.`).
			ArgDoc("name", `Name of the function, in its original format from the implementation language.`).
			ArgDoc("returnType", `Return type of the function.`),

		dagql.Func("sourceMap", s.sourceMap).
			Doc(`Creates source map metadata.`).
			ArgDoc("filename", "The filename from the module source.").
			ArgDoc("line", "The line number within the filename.").
			ArgDoc("column", "The column number within the line."),

		dagql.FuncWithCacheKey("currentModule", s.currentModule, core.CachePerClient).
			Doc(`The module currently being served in the session, if any.`),

		dagql.Func("currentTypeDefs", s.currentTypeDefs).
			// Impure for now, could use a finer grain cache key if we had the ability to mix
			// a digest of the dagql server schema into the cache key.
			Impure("Can change when modules are loaded into the schema.").
			Doc(`The TypeDef representations of the objects currently being served in the session.`),

		dagql.FuncWithCacheKey("currentFunctionCall", s.currentFunctionCall, core.CachePerClient).
			Doc(`The FunctionCall context that the SDK caller is currently executing in.`,
				`If the caller is not currently executing in a function, this will
				return an error.`),
	}.Install(s.dag)

	dagql.Fields[*core.FunctionCall]{
		dagql.FuncWithCacheKey("returnValue", s.functionCallReturnValue, core.CachePerClient).
			Doc(`Set the return value of the function call to the provided value.`).
			ArgDoc("value", `JSON serialization of the return value.`),
		dagql.FuncWithCacheKey("returnError", s.functionCallReturnError, core.CachePerClient).
			Doc(`Return an error from the function.`).
			ArgDoc("error", `The error to return.`),
	}.Install(s.dag)

	dagql.Fields[*core.Module]{
		// sync is used by external dependencies like daggerverse
		Syncer[*core.Module]().
			Doc(`Forces evaluation of the module, including any loading into the engine and associated validation.`),

		dagql.Func("dependencies", s.moduleDependencies).
			Doc(`The dependencies of the module.`),

		dagql.NodeFunc("generatedContextDirectory", s.moduleGeneratedContextDirectory).
			Doc(`The generated files and directories made on top of the module source's context directory.`),

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

		dagql.FuncWithCacheKey("workdir", s.currentModuleWorkdir, core.CachePerClient).
			Doc(`Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.`).
			ArgDoc("path", `Location of the directory to access (e.g., ".").`).
			ArgDoc("exclude", `Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`).
			ArgDoc("include", `Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),

		dagql.FuncWithCacheKey("workdirFile", s.currentModuleWorkdirFile, core.CachePerClient).
			Doc(`Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.`).
			ArgDoc("path", `Location of the file to retrieve (e.g., "README.md").`),
	}.Install(s.dag)

	dagql.Fields[*core.Function]{
		dagql.Func("withDescription", s.functionWithDescription).
			Doc(`Returns the function with the given doc string.`).
			ArgDoc("description", `The doc string to set.`),

		dagql.Func("withSourceMap", s.functionWithSourceMap).
			Doc(`Returns the function with the given source map.`).
			ArgDoc("sourceMap", `The source map for the function definition.`),

		dagql.Func("withArg", s.functionWithArg).
			Doc(`Returns the function with the provided argument`).
			ArgDoc("name", `The name of the argument`).
			ArgDoc("typeDef", `The type of the argument`).
			ArgDoc("description", `A doc string for the argument, if any`).
			ArgDoc("defaultValue", `A default value to use for this argument if not explicitly set by the caller, if any`).
			ArgDoc("defaultPath", `If the argument is a Directory or File type, default to load path from context directory, relative to root directory.`).
			ArgDoc("ignore", `Patterns to ignore when loading the contextual argument value.`),
	}.Install(s.dag)

	dagql.Fields[*core.FunctionArg]{}.Install(s.dag)

	dagql.Fields[*core.FunctionCallArgValue]{}.Install(s.dag)

	dagql.Fields[*core.SourceMap]{}.Install(s.dag)

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
			ArgDoc("description", `A doc string for the field, if any`).
			ArgDoc("sourceMap", `The source map for the field definition.`),

		dagql.Func("withFunction", s.typeDefWithFunction).
			Doc(`Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.`),

		dagql.Func("withConstructor", s.typeDefWithObjectConstructor).
			Doc(`Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.`),

		dagql.Func("withEnum", s.typeDefWithEnum).
			Doc(`Returns a TypeDef of kind Enum with the provided name.`,
				`Note that an enum's values may be omitted if the intent is only to refer to an enum.
				This is how functions are able to return their own, or any other circular reference.`).
			ArgDoc("name", `The name of the enum`).
			ArgDoc("description", `A doc string for the enum, if any`).
			ArgDoc("sourceMap", `The source map for the enum definition.`),

		dagql.Func("withEnumValue", s.typeDefWithEnumValue).
			Doc(`Adds a static value for an Enum TypeDef, failing if the type is not an enum.`).
			ArgDoc("value", `The name of the value in the enum`).
			ArgDoc("description", `A doc string for the value, if any`).
			ArgDoc("sourceMap", `The source map for the enum value definition.`),
	}.Install(s.dag)

	dagql.Fields[*core.ObjectTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.InterfaceTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.InputTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.FieldTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.ListTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.ScalarTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.EnumTypeDef]{}.Install(s.dag)
	dagql.Fields[*core.EnumValueTypeDef]{}.Install(s.dag)
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

func (s *moduleSchema) typeDefWithScalar(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("scalar type def must have a name")
	}
	return def.WithScalar(args.Name, args.Description), nil
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
	SourceMap   dagql.Optional[core.SourceMapID]
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("object type def must have a name")
	}
	sourceMap, err := s.loadSourceMap(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return def.WithObject(args.Name, args.Description, sourceMap), nil
}

func (s *moduleSchema) typeDefWithInterface(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("interface type def must have a name")
	}
	sourceMap, err := s.loadSourceMap(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return def.WithInterface(args.Name, args.Description, sourceMap), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
}) (*core.TypeDef, error) {
	fieldType, err := args.TypeDef.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	sourceMap, err := s.loadSourceMap(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return def.WithObjectField(args.Name, fieldType.Self, args.Description, sourceMap)
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

func (s *moduleSchema) typeDefWithEnum(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("enum type def must have a name")
	}
	sourceMap, err := s.loadSourceMap(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return def.WithEnum(args.Name, args.Description, sourceMap), nil
}

func (s *moduleSchema) typeDefWithEnumValue(ctx context.Context, def *core.TypeDef, args struct {
	Value       string
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
}) (*core.TypeDef, error) {
	if args.Value == "" {
		return nil, fmt.Errorf("enum value must not be empty")
	}
	sourceMap, err := s.loadSourceMap(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return def.WithEnumValue(args.Value, args.Description, sourceMap)
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

func (s *moduleSchema) sourceMap(ctx context.Context, _ *core.Query, args struct {
	Filename string
	Line     int
	Column   int
}) (*core.SourceMap, error) {
	return &core.SourceMap{
		Filename: args.Filename,
		Line:     args.Line,
		Column:   args.Column,
	}, nil
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
	DefaultPath  string    `default:""`
	Ignore       []string  `default:"[]"`
	SourceMap    dagql.Optional[core.SourceMapID]
}) (*core.Function, error) {
	argType, err := args.TypeDef.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}

	sourceMap, err := s.loadSourceMap(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}

	// Check if both values are used, return an error if so.
	if args.DefaultValue != nil && args.DefaultPath != "" {
		return nil, fmt.Errorf("cannot set both default value and default path from context")
	}

	// Check if default path from context is set for non-directory or non-file type
	if argType.Self.Kind == core.TypeDefKindObject && args.DefaultPath != "" &&
		(argType.Self.AsObject.Value.Name != "Directory" && argType.Self.AsObject.Value.Name != "File") {
		return nil, fmt.Errorf("can only set default path for Directory or File type, not %s", argType.Self.AsObject.Value.Name)
	}

	// Check if ignore is set for non-directory type
	if argType.Self.Kind == core.TypeDefKindObject &&
		len(args.Ignore) > 0 && argType.Self.AsObject.Value.Name != "Directory" {
		return nil, fmt.Errorf("can only set ignore for Directory type, not %s", argType.Self.AsObject.Value.Name)
	}

	// When using a default path SDKs can't set a default value and the argument
	// may be non-nullable, so we need to enforce it as optional.
	td := argType.Self
	if args.DefaultPath != "" {
		td = td.WithOptional(true)
	}

	return fn.WithArg(args.Name, td, args.Description, args.DefaultValue, args.DefaultPath, args.Ignore, sourceMap), nil
}

func (s *moduleSchema) functionWithSourceMap(ctx context.Context, fn *core.Function, args struct {
	SourceMap core.SourceMapID
}) (*core.Function, error) {
	sourceMap, err := args.SourceMap.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode source map: %w", err)
	}
	return fn.WithSourceMap(sourceMap.Self), nil
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

func (s *moduleSchema) functionCallReturnError(ctx context.Context, fnCall *core.FunctionCall, args struct {
	Error dagql.ID[*core.Error]
},
) (dagql.Nullable[core.Void], error) {
	// TODO: error out if caller is not coming from a module
	return dagql.Null[core.Void](), fnCall.ReturnError(ctx, args.Error)
}

func (s *moduleSchema) moduleGeneratedContextDirectory(
	ctx context.Context,
	mod dagql.Instance[*core.Module],
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	err = s.dag.Select(ctx, mod.Self.Source, &inst,
		dagql.Selector{
			Field: "generatedContextDirectory",
		},
	)
	return inst, err
}

func (s *moduleSchema) moduleDependencies(
	ctx context.Context,
	mod *core.Module,
	args struct{},
) ([]*core.Module, error) {
	depMods := make([]*core.Module, 0, len(mod.Deps.Mods))
	for _, dep := range mod.Deps.Mods {
		switch dep := dep.(type) {
		case *core.Module:
			depMods = append(depMods, dep)
		case *CoreMod:
			// skip
		default:
			return nil, fmt.Errorf("unexpected mod dependency type %T", dep)
		}
	}
	return depMods, nil
}

func (s *moduleSchema) moduleWithDescription(ctx context.Context, mod *core.Module, args struct {
	Description string
}) (*core.Module, error) {
	return mod.WithDescription(args.Description), nil
}

func (s *moduleSchema) moduleWithObject(ctx context.Context, mod *core.Module, args struct {
	Object core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Object.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return mod.WithObject(ctx, def.Self)
}

func (s *moduleSchema) moduleWithInterface(ctx context.Context, mod *core.Module, args struct {
	Iface core.TypeDefID
}) (_ *core.Module, rerr error) {
	def, err := args.Iface.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}
	return mod.WithInterface(ctx, def.Self)
}

func (s *moduleSchema) moduleWithEnum(ctx context.Context, mod *core.Module, args struct {
	Enum core.TypeDefID
}) (_ *core.Module, rerr error) {
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
	return curMod.Module.NameField, nil
}

func (s *moduleSchema) currentModuleSource(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct{},
) (inst dagql.Instance[*core.Directory], err error) {
	curSrc := curMod.Module.Source
	if curSrc.Self == nil {
		return inst, fmt.Errorf("module source not available during initialization")
	}

	srcSubpath := curSrc.Self.SourceSubpath
	if srcSubpath == "" {
		srcSubpath = curSrc.Self.SourceRootSubpath
	}

	var generatedDiff dagql.Instance[*core.Directory]
	err = s.dag.Select(ctx, curSrc, &generatedDiff,
		dagql.Selector{Field: "generatedContextDirectory"},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to get generated context directory: %w", err)
	}

	err = s.dag.Select(ctx, curSrc.Self.ContextDirectory, &inst,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "directory", Value: dagql.NewID[*core.Directory](generatedDiff.ID())},
			},
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(srcSubpath)},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to get source directory: %w", err)
	}

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

func (s *moduleSchema) loadSourceMap(ctx context.Context, sourceMap dagql.Optional[core.SourceMapID]) (*core.SourceMap, error) {
	if !sourceMap.Valid {
		return nil, nil
	}
	sourceMapI, err := sourceMap.Value.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode source map: %w", err)
	}
	return sourceMapI.Self, nil
}
