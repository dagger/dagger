package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/core/modules"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
)

type Module struct {
	Query *Query

	// The name of the module
	NameField string `field:"true" name:"name" doc:"The name of the module"`

	// The doc string of the module, if any
	Description *string `field:"true" doc:"The doc string of the module, if any"`

	// The module's SDKConfig, as set in the module config file
	SDKConfig string `field:"true" name:"sdk" doc:"The SDK used by this module. Either a name of a builtin SDK or a module ref pointing to the SDK's implementation."`

	// The module's source code root directory
	SourceDirectory dagql.Instance[*Directory] `field:"true" doc:"The directory containing the module's source code."`

	// If set, the subdir of the SourceDirectory that contains the module's source code
	SourceDirectorySubpath string `field:"true" doc:"The module's subpath within the source directory."`

	// Dependencies as configured by the module
	DependencyConfig []string `field:"true" doc:"The dependencies as configured by the module."`

	// The module's loaded dependencies.
	DependenciesField []dagql.Instance[*Module] `field:"true" name:"dependencies" doc:"Modules used by this module."`

	// GeneratedCode is the generated code for the module, which is available
	// even if it doesn't properly compile.
	GeneratedCode *GeneratedCode `field:"true" doc:"The code generated by the SDK's runtime."`

	// Deps contains the module's dependency DAG.
	Deps *ModDeps

	// The following are populated while initializing the module

	// The module's objects
	ObjectDefs []*TypeDef `field:"true" name:"objects" doc:"Objects served by this module."`

	// The module's interfaces
	InterfaceDefs []*TypeDef `field:"true" name:"interfaces" doc:"Interfaces served by this module."`

	// Runtime is the container that runs the module's entrypoint. It is
	// unavailable if the module doesn't compile.
	Runtime *Container

	// InstanceID is the ID of the initialized module.
	InstanceID *idproto.ID
}

func (*Module) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Module",
		NonNull:   true,
	}
}

func (*Module) TypeDescription() string {
	return "A Dagger module."
}

var _ Mod = (*Module)(nil)

func (mod *Module) Name() string {
	return mod.NameField
}

func (mod *Module) Dependencies() []Mod {
	mods := make([]Mod, len(mod.DependenciesField))
	for i, dep := range mod.DependenciesField {
		mods[i] = dep.Self
	}
	return mods
}

func (mod *Module) Initialize(ctx context.Context, oldSelf dagql.Instance[*Module], newID *idproto.ID) (*Module, error) {
	// construct a special function with no object or function name, which tells
	// the SDK to return the module's definition (in terms of objects, fields and
	// functions)
	getModDefFn, err := newModFunction(
		ctx,
		mod.Query,
		oldSelf.Self,
		oldSelf.ID(),
		nil,
		mod.Runtime,
		NewFunction("", &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(NewObjectTypeDef("Module", "")),
		}))
	if err != nil {
		return nil, fmt.Errorf("failed to create module definition function for module %q: %w", mod.Name(), err)
	}
	result, err := getModDefFn.Call(ctx, newID, &CallOpts{Cache: true, SkipSelfSchema: true})
	if err != nil {
		return nil, fmt.Errorf("failed to call module %q to get functions: %w", mod.Name(), err)
	}
	inst, ok := result.(dagql.Instance[*Module])
	if !ok {
		return nil, fmt.Errorf("expected Module result, got %T", result)
	}
	newMod := inst.Self.Clone()
	newMod.InstanceID = newID
	return newMod, nil
}

func (mod *Module) Install(ctx context.Context, dag *dagql.Server) error {
	slog.Debug("installing module", "name", mod.Name())
	start := time.Now()
	defer func() { slog.Debug("done installing module", "name", mod.Name(), "took", time.Since(start)) }()

	for _, def := range mod.ObjectDefs {
		objDef := def.AsObject.Value

		slog.Debug("installing object", "name", mod.Name(), "object", objDef.Name)

		// check whether this is a pre-existing object from a dependency module
		modType, ok, err := mod.Deps.ModTypeFor(ctx, def)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}

		if ok {
			// NB: this is defense-in-depth to prevent SDKs or some other future
			// component from allowing modules to extend external objects.

			slog.Warn("found existing module type",
				"name", mod.Name(),
				"object", objDef.Name,
				"sourceSame", modType.SourceMod() == mod,
				"definesFields", len(objDef.Fields) > 0,
				"definesFunctions", len(objDef.Functions) > 0,
			)

			if modType.SourceMod() != mod {
				// modules can reference types from core/other modules as types, but they
				// can't attach any new fields or functions to them
				//
				// TODO: what does "referencing" mean in this context? do we even get
				// here without fields or functions? is there any utility in defining
				// such a type in a module? if not, should we just simplify this to
				// some sort of name conflict error?
				if len(objDef.Fields) > 0 || len(objDef.Functions) > 0 {
					return fmt.Errorf("cannot attach new fields or functions to object %q from outside module", objDef.Name)
				}
				return nil
			}
		}

		obj := &ModuleObject{
			Module:  mod,
			TypeDef: objDef,
		}

		if err := obj.Install(ctx, dag); err != nil {
			return err
		}
	}

	for _, def := range mod.InterfaceDefs {
		ifaceDef := def.AsInterface.Value

		slog.Debug("installing interface", "name", mod.Name(), "interface", ifaceDef.Name)

		iface := &InterfaceType{
			typeDef: ifaceDef,
			mod:     mod,
		}

		if err := iface.Install(ctx, dag); err != nil {
			return err
		}
	}

	return nil
}

func (mod *Module) TypeDefs(ctx context.Context) ([]*TypeDef, error) {
	typeDefs := make([]*TypeDef, 0, len(mod.ObjectDefs)+len(mod.InterfaceDefs))
	for _, def := range mod.ObjectDefs {
		typeDef := def.Clone()
		if typeDef.AsObject.Valid {
			typeDef.AsObject.Value.SourceModuleName = mod.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}
	for _, def := range mod.InterfaceDefs {
		typeDef := def.Clone()
		if typeDef.AsInterface.Valid {
			typeDef.AsInterface.Value.SourceModuleName = mod.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}
	return typeDefs, nil
}

func (mod *Module) DependencySchemaIntrospectionJSON(ctx context.Context) (string, error) {
	return mod.Deps.SchemaIntrospectionJSON(ctx)
}

func (mod *Module) ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	var modType ModType
	switch typeDef.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindBoolean, TypeDefKindVoid:
		modType = &PrimitiveType{typeDef}

	case TypeDefKindList:
		underlyingType, ok, err := mod.ModTypeFor(ctx, typeDef.AsList.Value.ElementTypeDef, checkDirectDeps)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		modType = &ListType{
			Elem:       typeDef.AsList.Value.ElementTypeDef,
			Underlying: underlyingType,
		}

	case TypeDefKindObject:
		if checkDirectDeps {
			// check to see if this is from a *direct* dependency
			depType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get type from dependency: %w", err)
			}
			if ok {
				return depType, true, nil
			}
		}

		var found bool
		// otherwise it must be from this module
		for _, obj := range mod.ObjectDefs {
			if obj.AsObject.Value.Name == typeDef.AsObject.Value.Name {
				modType = &ModuleObjectType{
					typeDef: obj.AsObject.Value,
					mod:     mod,
				}
				found = true
				break
			}
		}
		if !found {
			slog.Debug("module did not find object", "mod", mod.Name(), "object", typeDef.AsObject.Value.Name)
			return nil, false, nil
		}

	case TypeDefKindInterface:
		if checkDirectDeps {
			// check to see if this is from a *direct* dependency
			depType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get interface type from dependency: %w", err)
			}
			if ok {
				return depType, true, nil
			}
		}

		var found bool
		// otherwise it must be from this module
		for _, iface := range mod.InterfaceDefs {
			if iface.AsInterface.Value.Name == typeDef.AsInterface.Value.Name {
				modType = &InterfaceType{
					mod:     mod,
					typeDef: iface.AsInterface.Value,
				}
				found = true
				break
			}
		}
		if !found {
			slog.Debug("module did not find interface", "mod", mod.Name(), "interface", typeDef.AsInterface.Value.Name)
			return nil, false, nil
		}

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}

	if typeDef.Optional {
		modType = &NullableType{
			Elem:       typeDef.WithOptional(false),
			Underlying: modType,
		}
	}

	return modType, true, nil
}

// verify the typedef is has no reserved names
func (mod *Module) validateTypeDef(ctx context.Context, typeDef *TypeDef) error {
	switch typeDef.Kind {
	case TypeDefKindList:
		return mod.validateTypeDef(ctx, typeDef.AsList.Value.ElementTypeDef)
	case TypeDefKindObject:
		return mod.validateObjectTypeDef(ctx, typeDef)
	case TypeDefKindInterface:
		return mod.validateInterfaceTypeDef(ctx, typeDef)
	}
	return nil
}

func (mod *Module) validateObjectTypeDef(ctx context.Context, typeDef *TypeDef) error {
	// check whether this is a pre-existing object from core or another module
	modType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
	if err != nil {
		return fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod != mod {
			// already validated, skip
			return nil
		}
	}

	obj := typeDef.AsObject.Value

	for _, field := range obj.Fields {
		if gqlFieldName(field.Name) == "id" {
			return fmt.Errorf("cannot define field with reserved name %q on object %q", field.Name, obj.Name)
		}
		fieldType, ok, err := mod.Deps.ModTypeFor(ctx, field.TypeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			if sourceMod := fieldType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod != mod {
				// already validated, skip
				return fmt.Errorf("object %q field %q cannot reference external type from dependency module %q",
					obj.OriginalName,
					field.OriginalName,
					sourceMod.Name(),
				)
			}
		}
		if err := mod.validateTypeDef(ctx, field.TypeDef); err != nil {
			return err
		}
	}

	for _, fn := range obj.Functions {
		if gqlFieldName(fn.Name) == "id" {
			return fmt.Errorf("cannot define function with reserved name %q on object %q", fn.Name, obj.Name)
		}
		// Check if this is a type from another (non-core) module, which is currently not allowed
		retType, ok, err := mod.Deps.ModTypeFor(ctx, fn.ReturnType)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			if sourceMod := retType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod != mod {
				// already validated, skip
				return fmt.Errorf("object %q function %q cannot return external type from dependency module %q",
					obj.OriginalName,
					fn.OriginalName,
					sourceMod.Name(),
				)
			}
		}
		if err := mod.validateTypeDef(ctx, fn.ReturnType); err != nil {
			return err
		}

		for _, arg := range fn.Args {
			if gqlArgName(arg.Name) == "id" {
				return fmt.Errorf("cannot define argument with reserved name %q on function %q", arg.Name, fn.Name)
			}
			argType, ok, err := mod.Deps.ModTypeFor(ctx, arg.TypeDef)
			if err != nil {
				return fmt.Errorf("failed to get mod type for type def: %w", err)
			}
			if ok {
				if sourceMod := argType.SourceMod(); sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod != mod {
					// already validated, skip
					return fmt.Errorf("object %q function %q arg %q cannot reference external type from dependency module %q",
						obj.OriginalName,
						fn.OriginalName,
						arg.OriginalName,
						sourceMod.Name(),
					)
				}
			}
			if err := mod.validateTypeDef(ctx, arg.TypeDef); err != nil {
				return err
			}
		}
	}
	return nil
}

func (mod *Module) validateInterfaceTypeDef(ctx context.Context, typeDef *TypeDef) error {
	iface := typeDef.AsInterface.Value

	// check whether this is a pre-existing interface from core or another module
	modType, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
	if err != nil {
		return fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod != mod {
			// already validated, skip
			return nil
		}
	}
	for _, fn := range iface.Functions {
		if gqlFieldName(fn.Name) == "id" {
			return fmt.Errorf("cannot define function with reserved name %q on interface %q", fn.Name, iface.Name)
		}
		if err := mod.validateTypeDef(ctx, fn.ReturnType); err != nil {
			return err
		}

		for _, arg := range fn.Args {
			if gqlArgName(arg.Name) == "id" {
				return fmt.Errorf("cannot define argument with reserved name %q on function %q", arg.Name, fn.Name)
			}
			if err := mod.validateTypeDef(ctx, arg.TypeDef); err != nil {
				return err
			}
		}
	}
	return nil
}

// prefix the given typedef (and any recursively referenced typedefs) with this module's name for any objects
func (mod *Module) namespaceTypeDef(ctx context.Context, typeDef *TypeDef) error {
	switch typeDef.Kind {
	case TypeDefKindList:
		if err := mod.namespaceTypeDef(ctx, typeDef.AsList.Value.ElementTypeDef); err != nil {
			return err
		}
	case TypeDefKindObject:
		obj := typeDef.AsObject.Value

		// only namespace objects defined in this module
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if !ok {
			obj.Name = namespaceObject(obj.Name, mod.Name())
		}

		for _, field := range obj.Fields {
			if err := mod.namespaceTypeDef(ctx, field.TypeDef); err != nil {
				return err
			}
		}

		for _, fn := range obj.Functions {
			if err := mod.namespaceTypeDef(ctx, fn.ReturnType); err != nil {
				return err
			}

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, arg.TypeDef); err != nil {
					return err
				}
			}
		}
	case TypeDefKindInterface:
		iface := typeDef.AsInterface.Value

		// only namespace interfaces defined in this module
		_, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if !ok {
			iface.Name = namespaceObject(iface.Name, mod.Name())
		}

		for _, fn := range iface.Functions {
			if err := mod.namespaceTypeDef(ctx, fn.ReturnType); err != nil {
				return err
			}

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, arg.TypeDef); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

/*
Mod is a module in loaded into the server's DAG of modules; it's the vertex type of the DAG.
It's an interface so we can abstract over user modules and core and treat them the same.
*/
type Mod interface {
	// The name of the module
	Name() string

	// The direct dependencies of this module
	Dependencies() []Mod

	// TODO describe
	Install(context.Context, *dagql.Server) error

	// ModTypeFor returns the ModType for the given typedef based on this module's schema.
	// The returned type will have any namespacing already applied.
	// If checkDirectDeps is true, then its direct dependencies will also be checked.
	ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error)

	// All the TypeDefs exposed by this module (does not include dependencies)
	TypeDefs(ctx context.Context) ([]*TypeDef, error)
}

/*
An SDK is an implementation of the functionality needed to generate code for and execute a module.

There is one special SDK, the Go SDK, which is implemented in `goSDK` below. It's used as the "seed" for all
other SDK implementations.

All other SDKs are themselves implemented as Modules, with Functions matching the two defined in this SDK interface.

An SDK Module needs to choose its own SDK for its implementation. This can be "well-known" built-in SDKs like "go",
"python", etc. Or it can be any external module as specified with a module ref.

You can thus think of SDK Modules as a DAG of dependencies, with each SDK using a different SDK to implement its Module,
with the Go SDK as the root of the DAG and the only one without any dependencies.

Built-in SDKs are also a bit special in that they come bundled w/ the engine container image, which allows them
to be used without hard dependencies on the internet. They are loaded w/ the `loadBuiltinSDK` function below, which
loads them as modules from the engine container.
*/
type SDK interface {
	/* Codegen generates code for the module at the given source directory and subpath.

	The Code field of the returned GeneratedCode object should be the generated contents of the module sourceDirSubpath,
	in the case where that's different than the root of the sourceDir.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Codegen(context.Context, *Module, dagql.Instance[*Directory], string) (*GeneratedCode, error)

	/* Runtime returns a container that is used to execute module code at runtime in the Dagger engine.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Runtime(context.Context, *Module, dagql.Instance[*Directory], string) (*Container, error)
}

var _ HasPBDefinitions = (*Module)(nil)

func (mod *Module) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if mod.SourceDirectory.Self != nil {
		dirDefs, err := mod.SourceDirectory.Self.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		defs = append(defs, dirDefs...)
	}
	return defs, nil
}

func (mod Module) Clone() *Module {
	cp := mod
	if mod.SourceDirectory.Self != nil {
		cp.SourceDirectory.Self = mod.SourceDirectory.Self.Clone()
	}
	cp.DependencyConfig = cloneSlice(mod.DependencyConfig)
	cp.ObjectDefs = make([]*TypeDef, len(mod.ObjectDefs))
	for i, def := range mod.ObjectDefs {
		cp.ObjectDefs[i] = def.Clone()
	}
	cp.InterfaceDefs = make([]*TypeDef, len(mod.InterfaceDefs))
	for i, def := range mod.InterfaceDefs {
		cp.InterfaceDefs[i] = def.Clone()
	}
	return &cp
}

func (mod *Module) WithObject(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsObject.Valid {
		return nil, fmt.Errorf("expected object type def, got %s: %+v", def.Kind, def)
	}
	if err := mod.validateTypeDef(ctx, def); err != nil {
		return nil, fmt.Errorf("failed to validate type def: %w", err)
	}
	def = def.Clone()
	if err := mod.namespaceTypeDef(ctx, def); err != nil {
		return nil, fmt.Errorf("failed to namespace type def: %w", err)
	}
	mod.ObjectDefs = append(mod.ObjectDefs, def)
	return mod, nil
}

func (mod *Module) WithInterface(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsInterface.Valid {
		return nil, fmt.Errorf("expected interface type def, got %s: %+v", def.Kind, def)
	}
	if err := mod.validateTypeDef(ctx, def); err != nil {
		return nil, fmt.Errorf("failed to validate type def: %w", err)
	}
	def = def.Clone()
	if err := mod.namespaceTypeDef(ctx, def); err != nil {
		return nil, fmt.Errorf("failed to namespace type def: %w", err)
	}
	mod.InterfaceDefs = append(mod.InterfaceDefs, def)
	return mod, nil
}

// Load the module config as parsed from the given File
func LoadModuleConfigFromFile(
	ctx context.Context,
	configFile *File,
) (*modules.Config, error) {
	configBytes, err := configFile.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg modules.Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &cfg, nil
}

// Load the module config from the module from the given diretory at the given path
func LoadModuleConfig(
	ctx context.Context,
	sourceDir *Directory,
	configPath string,
) (string, *modules.Config, error) {
	configPath = modules.NormalizeConfigPath(configPath)
	configFile, err := sourceDir.File(ctx, configPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get config file from path %q: %w", configPath, err)
	}
	cfg, err := LoadModuleConfigFromFile(ctx, configFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load config: %w", err)
	}
	return configPath, cfg, nil
}

func LoadRef(
	ctx context.Context,
	srv *dagql.Server,
	sourceDir dagql.Instance[*Directory],
	subPath string,
	ref string,
) (dagql.Instance[*Module], error) {
	var dep dagql.Instance[*Module]
	modRef, err := modules.ResolveStableRef(ref)
	if err != nil {
		return dep, fmt.Errorf("failed to parse dependency url %q: %w", ref, err)
	}
	switch {
	case modRef.Local:
		depPath := filepath.Join(subPath, modRef.Path)
		if strings.HasPrefix(depPath+"/", "../") {
			return dep, fmt.Errorf("local module path %q is not under root", modRef.Path)
		}
		err := srv.Select(ctx, sourceDir, &dep, dagql.Selector{
			Field: "asModule",
			Args: []dagql.NamedInput{
				{Name: "sourceSubpath", Value: dagql.String(depPath)},
			},
		})
		if err != nil {
			return dep, fmt.Errorf("load %q: %w", ref, err)
		}
	case modRef.Git != nil:
		err := srv.Select(ctx, srv.Root(), &dep, dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(modRef.Git.CloneURL)},
			},
		}, dagql.Selector{
			Field: "commit",
			Args: []dagql.NamedInput{
				{Name: "id", Value: dagql.String(modRef.Version)},
			},
		}, dagql.Selector{
			Field: "tree",
		}, dagql.Selector{
			Field: "asModule",
			Args: []dagql.NamedInput{
				{Name: "sourceSubpath", Value: dagql.String(modRef.SubPath)},
			},
		})
		if err != nil {
			return dep, fmt.Errorf("load %q: %w", ref, err)
		}
	default:
		return dep, fmt.Errorf("unsupported module ref %q", ref)
	}
	return dep, nil
}
