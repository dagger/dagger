package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

type Module struct {
	Query *Query

	// The source of the module
	Source dagql.Instance[*ModuleSource] `field:"true" name:"source" doc:"The source for the module."`

	// The name of the module
	NameField string `field:"true" name:"name" doc:"The name of the module"`

	// The original name of the module set in its configuration file (or first configured via withName).
	// Different than NameField when a different name was specified for the module via a dependency.
	OriginalName string

	// The origin sdk of the module set in its configuration file (or first configured via withSDK).
	OriginalSDK string

	// The doc string of the module, if any
	Description string `field:"true" doc:"The doc string of the module, if any"`

	// The module's SDKConfig, as set in the module config file
	SDKConfig string `field:"true" name:"sdk" doc:"The SDK used by this module. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation."`

	GeneratedContextDirectory dagql.Instance[*Directory] `field:"true" name:"generatedContextDirectory" doc:"The module source's context plus any configuration and source files created by codegen."`

	// Dependencies as configured by the module
	DependencyConfig []*ModuleDependency `field:"true" doc:"The dependencies as configured by the module."`

	// The module's loaded dependencies, not yet initialized
	DependenciesField []dagql.Instance[*Module] `field:"true" name:"dependencies" doc:"Modules used by this module."`

	// Deps contains the module's dependency DAG.
	Deps *ModDeps

	// Runtime is the container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
	Runtime *Container `field:"true" name:"runtime" doc:"The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile."`

	// The following are populated while initializing the module

	// The module's objects
	ObjectDefs []*TypeDef `field:"true" name:"objects" doc:"Objects served by this module."`

	// The module's interfaces
	InterfaceDefs []*TypeDef `field:"true" name:"interfaces" doc:"Interfaces served by this module."`

	// The module's enumerations
	EnumDefs []*TypeDef `field:"true" name:"enums" doc:"Enumerations served by this module."`

	// InstanceID is the ID of the initialized module.
	InstanceID *call.ID
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

type ModuleDependency struct {
	Source dagql.Instance[*ModuleSource] `field:"true" name:"source" doc:"The source for the dependency module."`
	Name   string                        `field:"true" name:"name" doc:"The name of the dependency module."`
}

func (*ModuleDependency) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleDependency",
		NonNull:   true,
	}
}

func (*ModuleDependency) TypeDescription() string {
	return "The configuration of dependency of a module."
}

func (dep ModuleDependency) Clone() *ModuleDependency {
	cp := dep
	cp.Source.Self = dep.Source.Self.Clone()
	return &cp
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

func (mod *Module) IDModule() *call.Module {
	ref, err := mod.Source.Self.RefString()
	if err != nil {
		// TODO: this should be impossible by not, right? doesn't seem worth
		// propagating error
		panic(err)
	}
	return call.NewModule(mod.InstanceID, mod.Name(), ref)
}

func (mod *Module) Initialize(ctx context.Context, oldID *call.ID, newID *call.ID) (*Module, error) {
	modName := mod.Name()
	newMod := mod.Clone()
	newMod.InstanceID = oldID // updated to newID once the call to initialize is done

	// construct a special function with no object or function name, which tells
	// the SDK to return the module's definition (in terms of objects, fields and
	// functions)
	getModDefFn, err := newModFunction(
		ctx,
		newMod.Query,
		newMod,
		nil,
		newMod.Runtime,
		NewFunction("", &TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(NewObjectTypeDef("Module", "")),
		}))
	if err != nil {
		return nil, fmt.Errorf("failed to create module definition function for module %q: %w", modName, err)
	}

	result, err := getModDefFn.Call(ctx, &CallOpts{Cache: true, SkipSelfSchema: true})
	if err != nil {
		return nil, fmt.Errorf("failed to call module %q to get functions: %w", modName, err)
	}
	inst, ok := result.(dagql.Instance[*Module])
	if !ok {
		return nil, fmt.Errorf("expected Module result, got %T", result)
	}

	newMod.InstanceID = newID
	newMod.Description = inst.Self.Description
	for _, obj := range inst.Self.ObjectDefs {
		newMod, err = newMod.WithObject(ctx, obj)
		if err != nil {
			return nil, fmt.Errorf("failed to add object to module %q: %w", modName, err)
		}
	}
	for _, iface := range inst.Self.InterfaceDefs {
		newMod, err = newMod.WithInterface(ctx, iface)
		if err != nil {
			return nil, fmt.Errorf("failed to add interface to module %q: %w", modName, err)
		}
	}
	for _, enum := range inst.Self.EnumDefs {
		newMod, err = newMod.WithEnum(ctx, enum)
		if err != nil {
			return nil, fmt.Errorf("failed to add enum to module %q: %w", mod.Name(), err)
		}
	}
	newMod.InstanceID = newID

	return newMod, nil
}

func (mod *Module) Install(ctx context.Context, dag *dagql.Server) error {
	slog.ExtraDebug("installing module", "name", mod.Name())
	start := time.Now()
	defer func() { slog.ExtraDebug("done installing module", "name", mod.Name(), "took", time.Since(start)) }()

	for _, def := range mod.ObjectDefs {
		objDef := def.AsObject.Value

		slog.ExtraDebug("installing object", "name", mod.Name(), "object", objDef.Name)

		// check whether this is a pre-existing object from a dependency module
		modType, ok, err := mod.Deps.ModTypeFor(ctx, def)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}

		if ok {
			// NB: this is defense-in-depth to prevent SDKs or some other future
			// component from allowing modules to extend external objects.
			return fmt.Errorf("type %q is already defined by module %q",
				objDef.Name,
				modType.SourceMod().Name())
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

		slog.ExtraDebug("installing interface", "name", mod.Name(), "interface", ifaceDef.Name)

		iface := &InterfaceType{
			typeDef: ifaceDef,
			mod:     mod,
		}

		if err := iface.Install(ctx, dag); err != nil {
			return err
		}
	}

	for _, def := range mod.EnumDefs {
		enumDef := def.AsEnum.Value

		slog.ExtraDebug("installing enum", "name", mod.Name(), "enum", enumDef.Name)

		enum := &EnumObject{
			Module:  mod,
			TypeDef: enumDef,
		}

		if err := enum.Install(ctx, dag); err != nil {
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
			slog.ExtraDebug("module did not find object", "mod", mod.Name(), "object", typeDef.AsObject.Value.Name)
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
			slog.ExtraDebug("module did not find interface", "mod", mod.Name(), "interface", typeDef.AsInterface.Value.Name)
			return nil, false, nil
		}

	case TypeDefKindScalar:
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

		slog.ExtraDebug("module did not find scalar", "mod", mod.Name(), "scalar", typeDef.AsScalar.Value.Name)
		return nil, false, nil

	case TypeDefKindEnum:
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

		// otherwise it must be from this module
		var found bool
		for _, enum := range mod.EnumDefs {
			if enum.AsEnum.Value.Name == typeDef.AsEnum.Value.Name {
				modType = &ModuleEnumType{
					mod:     mod,
					typeDef: enum.AsEnum.Value,
				}
				found = true
				break
			}
		}

		if !found {
			slog.ExtraDebug("module did not find enum", "mod", mod.Name(), "enum", typeDef.AsEnum.Value.Name)
			return nil, false, nil
		}

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}

	if typeDef.Optional {
		modType = &NullableType{
			InnerDef: typeDef.WithOptional(false),
			Inner:    modType,
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
			sourceMod := fieldType.SourceMod()
			// fields can reference core types and local types, but not types from
			// other modules
			if sourceMod != nil && sourceMod.Name() != ModuleName && sourceMod != mod {
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
			obj.Name = namespaceObject(obj.OriginalName, mod.Name(), mod.OriginalName)
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
			iface.Name = namespaceObject(iface.OriginalName, mod.Name(), mod.OriginalName)
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
"python", etc. Or it can be any external module as specified with a module source ref string.

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
	Codegen(context.Context, *ModDeps, dagql.Instance[*ModuleSource]) (*GeneratedCode, error)

	/* Runtime returns a container that is used to execute module code at runtime in the Dagger engine.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Runtime(context.Context, *ModDeps, dagql.Instance[*ModuleSource]) (*Container, error)

	// Paths that should always be loaded from module sources using this SDK. Ensures that e.g. main.go
	// in the Go SDK is always loaded even if dagger.json has include settings that don't include it.
	RequiredPaths(context.Context) ([]string, error)
}

var _ HasPBDefinitions = (*Module)(nil)

func (mod *Module) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if mod.Source.Self != nil {
		dirDefs, err := mod.Source.Self.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		defs = append(defs, dirDefs...)
	}
	return defs, nil
}

func (mod Module) Clone() *Module {
	cp := mod

	if mod.Query != nil {
		cp.Query = mod.Query.Clone()
	}

	if mod.Source.Self != nil {
		cp.Source.Self = mod.Source.Self.Clone()
	}

	cp.DependencyConfig = make([]*ModuleDependency, len(mod.DependencyConfig))
	for i, dep := range mod.DependencyConfig {
		cp.DependencyConfig[i] = dep.Clone()
	}

	cp.DependenciesField = make([]dagql.Instance[*Module], len(mod.DependenciesField))
	for i, dep := range mod.DependenciesField {
		cp.DependenciesField[i].Self = dep.Self.Clone()
	}

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

func (mod *Module) WithDescription(desc string) *Module {
	mod = mod.Clone()
	mod.Description = strings.TrimSpace(desc)
	return mod
}

func (mod *Module) WithObject(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsObject.Valid {
		return nil, fmt.Errorf("expected object type def, got %s: %+v", def.Kind, def)
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		def = def.Clone()
		if err := mod.namespaceTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.ObjectDefs = append(mod.ObjectDefs, def)
	return mod, nil
}

func (mod *Module) WithInterface(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsInterface.Valid {
		return nil, fmt.Errorf("expected interface type def, got %s: %+v", def.Kind, def)
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		def = def.Clone()
		if err := mod.namespaceTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.InterfaceDefs = append(mod.InterfaceDefs, def)
	return mod, nil
}

func (mod *Module) WithEnum(ctx context.Context, def *TypeDef) (*Module, error) {
	mod = mod.Clone()
	if !def.AsEnum.Valid {
		return nil, fmt.Errorf("expected enum type def, got %s: %+v", def.Kind, def)
	}

	// skip validation+namespacing for module objects being constructed by SDK with* calls
	// they will be validated when merged into the real final module

	if mod.Deps != nil {
		if err := mod.validateTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}
	}
	if mod.NameField != "" {
		def = def.Clone()
		if err := mod.namespaceTypeDef(ctx, def); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}
	}

	mod.EnumDefs = append(mod.EnumDefs, def)

	return mod, nil
}

type CurrentModule struct {
	Module *Module
}

func (*CurrentModule) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CurrentModule",
		NonNull:   true,
	}
}

func (*CurrentModule) TypeDescription() string {
	return "Reflective module API provided to functions at runtime."
}

func (mod CurrentModule) Clone() *CurrentModule {
	cp := mod
	cp.Module = mod.Module.Clone()
	return &cp
}
