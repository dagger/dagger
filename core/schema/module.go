package schema

import (
	"context"
	"errors"
	"fmt"
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

		dagql.Func("moduleConfig", s.moduleConfig).
			Doc(`Load the static configuration for a module from the given source directory and optional subpath.`),

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

	dagql.Fields[*core.Module]{
		dagql.Func("withSource", s.moduleWithSource).
			Doc(`Retrieves the module with basic configuration loaded, ready for initialization.`).
			ArgDoc("directory", `The directory containing the module's source code.`).
			ArgDoc("subpath",
				`An optional subpath of the directory which contains the module's
				source code.`,
				`This is needed when the module code is in a subdirectory but requires
				parent directories to be loaded in order to execute. For example, the
				module source code may need a go.mod, project.toml, package.json, etc.
				file from a parent directory.`,
				`If not set, the module source code is loaded from the root of the
				directory.`),

		dagql.NodeFunc("initialize", s.moduleInitialize).
			Doc(`Retrieves the module with the objects loaded via its SDK.`),

		dagql.Func("withDescription", s.moduleWithDescription).
			Doc(`Retrieves the module with the given description`).
			ArgDoc("description", `The description to set`),

		dagql.Func("withObject", s.moduleWithObject).
			Doc(`This module plus the given Object type and associated functions.`),

		dagql.Func("withInterface", s.moduleWithInterface).
			Doc(`This module plus the given Interface type and associated functions`),

		dagql.NodeFunc("serve", s.moduleServe).
			Impure(`Mutates the calling session's global schema.`).
			Doc(`Serve a module's API in the current session.`,
				`Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.`),
	}.Install(s.dag)

	dagql.Fields[*modules.Config]{}.Install(s.dag)

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
		dagql.Func("withVCSIgnoredPaths", s.generatedCodeWithVCSIgnoredPaths).
			Doc(`Set the list of paths to ignore in version control.`),
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
	return core.NewGeneratedCode(dir.Self), nil
}

func (s *moduleSchema) generatedCodeWithVCSIgnoredPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSIgnoredPaths(args.Paths), nil
}

func (s *moduleSchema) generatedCodeWithVCSGeneratedPaths(ctx context.Context, code *core.GeneratedCode, args struct {
	Paths []string
}) (*core.GeneratedCode, error) {
	return code.WithVCSGeneratedPaths(args.Paths), nil
}

func (s *moduleSchema) module(ctx context.Context, query *core.Query, _ struct{}) (*core.Module, error) {
	return query.NewModule(), nil
}

type moduleConfigArgs struct {
	SourceDirectory core.DirectoryID
	Subpath         string `default:""`
}

func (s *moduleSchema) moduleConfig(ctx context.Context, query *core.Query, args moduleConfigArgs) (*modules.Config, error) {
	srcDir, err := args.SourceDirectory.Load(ctx, s.dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode source directory: %w", err)
	}

	_, cfg, err := core.LoadModuleConfig(ctx, srcDir.Self, args.Subpath)
	return cfg, err
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

func (s *moduleSchema) moduleWithSource(ctx context.Context, self *core.Module, args struct {
	Directory core.DirectoryID
	Subpath   string `default:""`
}) (_ *core.Module, rerr error) {
	sourceDir, err := args.Directory.Load(ctx, s.dag)
	if err != nil {
		return nil, err
	}

	configPath, cfg, err := core.LoadModuleConfig(ctx, sourceDir.Self, args.Subpath)
	if err != nil {
		return nil, err
	}

	// Reposition the root of the sourceDir in case it's pointing to a subdir of current sourceDir
	if filepath.Clean(cfg.Root) != "." {
		rootPath := filepath.Join(filepath.Dir(configPath), cfg.Root)
		if rootPath != filepath.Dir(configPath) {
			configPathAbs, err := filepath.Abs(configPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get config absolute path: %w", err)
			}
			rootPathAbs, err := filepath.Abs(rootPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get root absolute path: %w", err)
			}
			configPath, err = filepath.Rel(rootPathAbs, configPathAbs)
			if err != nil {
				return nil, fmt.Errorf("failed to get config relative to root: %w", err)
			}
			if strings.HasPrefix(configPath, "../") {
				// this likely shouldn't happen, a client shouldn't submit a
				// module config that escapes the module root
				return nil, fmt.Errorf("module subpath is not under module root")
			}
			if rootPath != "." {
				err = s.dag.Select(ctx, sourceDir, &sourceDir, dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(rootPath)},
					},
				})
				if err != nil {
					return nil, fmt.Errorf("failed to get root directory: %w", err)
				}
			}
		}
	}

	sourceDirSubpath := filepath.Dir(configPath)

	var eg errgroup.Group
	deps := make([]dagql.Instance[*core.Module], len(cfg.Dependencies))
	for i, depRef := range cfg.Dependencies {
		i, depRef := i, depRef
		eg.Go(func() error {
			dep, err := core.LoadRef(ctx, s.dag, sourceDir, sourceDirSubpath, depRef)
			if err != nil {
				return err
			}
			deps[i] = dep
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		if errors.Is(err, dagql.ErrCacheMapRecursiveCall) {
			err = fmt.Errorf("module %s has a circular dependency: %w", cfg.Name, err)
		}
		return nil, err
	}

	self = self.Clone()
	self.NameField = cfg.Name
	self.DependencyConfig = cfg.Dependencies
	self.SDKConfig = cfg.SDK
	self.SourceDirectory = sourceDir
	self.SourceDirectorySubpath = sourceDirSubpath
	self.DependenciesField = deps

	self.Deps = core.NewModDeps(self.Query, self.Dependencies()).
		Append(self.Query.DefaultDeps.Mods...)

	sdk, err := s.sdkForModule(ctx, self.Query, cfg.SDK, sourceDir, sourceDirSubpath)
	if err != nil {
		return nil, err
	}

	self.GeneratedCode, err = sdk.Codegen(ctx, self, sourceDir, sourceDirSubpath)
	if err != nil {
		return nil, err
	}

	self.Runtime, err = sdk.Runtime(ctx, self, sourceDir, sourceDirSubpath)
	if err != nil {
		return nil, fmt.Errorf("failed to get module runtime: %w", err)
	}

	return self, nil
}

func (s *moduleSchema) moduleInitialize(ctx context.Context, inst dagql.Instance[*core.Module], args struct{}) (*core.Module, error) {
	return inst.Self.Initialize(ctx, inst, dagql.CurrentID(ctx))
}

type asModuleArgs struct {
	SourceSubpath string `default:""`
}

func (s *moduleSchema) directoryAsModule(ctx context.Context, sourceDir dagql.Instance[*core.Directory], args asModuleArgs) (inst dagql.Instance[*core.Module], rerr error) {
	rerr = s.dag.Select(ctx, s.dag.Root(), &inst, dagql.Selector{
		Field: "module",
	}, dagql.Selector{
		Field: "withSource",
		Args: []dagql.NamedInput{
			{Name: "directory", Value: dagql.NewID[*core.Directory](sourceDir.ID())},
			{Name: "subpath", Value: dagql.String(args.SourceSubpath)},
		},
	}, dagql.Selector{
		Field: "initialize",
	})
	return
}

func (s *moduleSchema) currentModule(ctx context.Context, self *core.Query, _ struct{}) (inst dagql.Instance[*core.Module], err error) {
	id, err := self.CurrentModule(ctx)
	if err != nil {
		return inst, err
	}
	return id.Load(ctx, s.dag)
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
