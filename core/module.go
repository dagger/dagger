package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
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

	// The module's SDKConfig, as set in the module config file
	SDKConfig *SDKConfig `field:"true" name:"sdk" doc:"The SDK config used by this module."`

	// Deps contains the module's dependency DAG.
	Deps *ModDeps

	// Runtime is the container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
	Runtime *Container `field:"true" name:"runtime" doc:"The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile."`

	// The following are populated while initializing the module

	// The doc string of the module, if any
	Description string `field:"true" doc:"The doc string of the module, if any"`

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

var _ Mod = (*Module)(nil)

func (mod *Module) Name() string {
	return mod.NameField
}

func (mod *Module) GetSource() *ModuleSource {
	return mod.Source.Self
}

func (mod *Module) IDModule() *call.Module {
	var ref, pin string
	switch mod.Source.Self.Kind {
	case ModuleSourceKindLocal:
		ref = filepath.Join(mod.Source.Self.Local.ContextDirectoryPath, mod.Source.Self.SourceRootSubpath)

	case ModuleSourceKindGit:
		ref = mod.Source.Self.Git.CloneRef
		if mod.Source.Self.SourceRootSubpath != "" {
			ref += "/" + strings.TrimPrefix(mod.Source.Self.SourceRootSubpath, "/")
		}
		if mod.Source.Self.Git.Version != "" {
			ref += "@" + mod.Source.Self.Git.Version
		}
		pin = mod.Source.Self.Git.Commit

	case ModuleSourceKindDir:
		// FIXME: this is better than nothing, but no other code handles refs that
		// are an encoded ID right now
		var err error
		ref, err = mod.Source.Self.ContextDirectory.ID().Encode()
		if err != nil {
			panic(fmt.Sprintf("failed to encode context directory ID: %v", err))
		}

	default:
		panic(fmt.Sprintf("unexpected module source kind %q", mod.Source.Self.Kind))
	}

	return call.NewModule(mod.InstanceID, mod.Name(), ref, pin)
}

func (mod *Module) Evaluate(context.Context) (*buildkit.Result, error) {
	return nil, nil
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

		slog.ExtraDebug("installing enum", "name", mod.Name(), "enum", enumDef.Name, "values", len(enumDef.Values))

		enum := &ModuleEnum{
			TypeDef: enumDef,
		}
		enum.Install(dag)
	}

	return nil
}

func (mod *Module) TypeDefs(ctx context.Context) ([]*TypeDef, error) {
	typeDefs := make([]*TypeDef, 0, len(mod.ObjectDefs)+len(mod.InterfaceDefs)+len(mod.EnumDefs))

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

	for _, def := range mod.EnumDefs {
		typeDef := def.Clone()
		if typeDef.AsEnum.Valid {
			typeDef.AsEnum.Value.SourceModuleName = mod.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}

	return typeDefs, nil
}

func (mod *Module) View() (string, bool) {
	return "", false
}

func (mod *Module) ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	var modType ModType
	var ok bool
	var err error

	switch typeDef.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean, TypeDefKindVoid:
		modType, ok = mod.modTypeForPrimitive(typeDef)
	case TypeDefKindList:
		modType, ok, err = mod.modTypeForList(ctx, typeDef, checkDirectDeps)
	case TypeDefKindObject:
		modType, ok, err = mod.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = mod.modTypeForObject(typeDef)
	case TypeDefKindInterface:
		modType, ok, err = mod.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = mod.modTypeForInterface(typeDef)
	case TypeDefKindScalar:
		modType, ok, err = mod.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = nil, false
		slog.ExtraDebug("module did not find scalar", "mod", mod.Name(), "scalar", typeDef.AsScalar.Value.Name)
	case TypeDefKindEnum:
		modType, ok, err = mod.modTypeFromDeps(ctx, typeDef, checkDirectDeps)
		if ok || err != nil {
			return modType, ok, err
		}
		modType, ok = mod.modTypeForEnum(typeDef)
	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}

	if err != nil {
		return nil, false, fmt.Errorf("failed to get mod type: %w", err)
	}

	if !ok {
		return nil, false, nil
	}

	if typeDef.Optional {
		modType = &NullableType{
			InnerDef: typeDef.WithOptional(false),
			Inner:    modType,
		}
	}

	return modType, true, nil
}

func (mod *Module) modTypeFromDeps(ctx context.Context, typedef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	if !checkDirectDeps {
		return nil, false, nil
	}

	// check to see if this is from a *direct* dependency
	depType, ok, err := mod.Deps.ModTypeFor(ctx, typedef)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get %s from dependency: %w", typedef.Kind, err)
	}
	return depType, ok, nil
}

func (mod *Module) modTypeForPrimitive(typedef *TypeDef) (ModType, bool) {
	return &PrimitiveType{typedef}, true
}

func (mod *Module) modTypeForList(ctx context.Context, typedef *TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	underlyingType, ok, err := mod.ModTypeFor(ctx, typedef.AsList.Value.ElementTypeDef, checkDirectDeps)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
	}
	if !ok {
		return nil, false, nil
	}

	return &ListType{
		Elem:       typedef.AsList.Value.ElementTypeDef,
		Underlying: underlyingType,
	}, true, nil
}

func (mod *Module) modTypeForObject(typeDef *TypeDef) (ModType, bool) {
	for _, obj := range mod.ObjectDefs {
		if obj.AsObject.Value.Name == typeDef.AsObject.Value.Name {
			return &ModuleObjectType{
				typeDef: obj.AsObject.Value,
				mod:     mod,
			}, true
		}
	}

	slog.ExtraDebug("module did not find object", "mod", mod.Name(), "object", typeDef.AsObject.Value.Name)
	return nil, false
}

func (mod *Module) modTypeForInterface(typeDef *TypeDef) (ModType, bool) {
	for _, iface := range mod.InterfaceDefs {
		if iface.AsInterface.Value.Name == typeDef.AsInterface.Value.Name {
			return &InterfaceType{
				typeDef: iface.AsInterface.Value,
				mod:     mod,
			}, true
		}
	}

	slog.ExtraDebug("module did not find interface", "mod", mod.Name(), "interface", typeDef.AsInterface.Value.Name)
	return nil, false
}

func (mod *Module) modTypeForEnum(typeDef *TypeDef) (ModType, bool) {
	for _, enum := range mod.EnumDefs {
		if enum.AsEnum.Value.Name == typeDef.AsEnum.Value.Name {
			return &ModuleEnumType{
				typeDef: enum.AsEnum.Value,
				mod:     mod,
			}, true
		}
	}

	slog.ExtraDebug("module did not find enum", "mod", mod.Name(), "enum", typeDef.AsEnum.Value.Name)
	return nil, false
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

// prefix the given typedef (and any recursively referenced typedefs) with this
// module's name/path for any objects
func (mod *Module) namespaceTypeDef(ctx context.Context, modPath string, typeDef *TypeDef) error {
	switch typeDef.Kind {
	case TypeDefKindList:
		if err := mod.namespaceTypeDef(ctx, modPath, typeDef.AsList.Value.ElementTypeDef); err != nil {
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
			obj.SourceMap = mod.namespaceSourceMap(modPath, obj.SourceMap)
		}

		for _, field := range obj.Fields {
			if err := mod.namespaceTypeDef(ctx, modPath, field.TypeDef); err != nil {
				return err
			}
			field.SourceMap = mod.namespaceSourceMap(modPath, field.SourceMap)
		}

		for _, fn := range obj.Functions {
			if err := mod.namespaceTypeDef(ctx, modPath, fn.ReturnType); err != nil {
				return err
			}
			fn.SourceMap = mod.namespaceSourceMap(modPath, fn.SourceMap)

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, modPath, arg.TypeDef); err != nil {
					return err
				}
				arg.SourceMap = mod.namespaceSourceMap(modPath, arg.SourceMap)
			}
		}

		if obj.Constructor.Valid {
			fn := obj.Constructor.Value
			if err := mod.namespaceTypeDef(ctx, modPath, fn.ReturnType); err != nil {
				return err
			}
			fn.SourceMap = mod.namespaceSourceMap(modPath, fn.SourceMap)

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, modPath, arg.TypeDef); err != nil {
					return err
				}
				arg.SourceMap = mod.namespaceSourceMap(modPath, arg.SourceMap)
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
			iface.SourceMap = mod.namespaceSourceMap(modPath, iface.SourceMap)
		}

		for _, fn := range iface.Functions {
			if err := mod.namespaceTypeDef(ctx, modPath, fn.ReturnType); err != nil {
				return err
			}
			fn.SourceMap = mod.namespaceSourceMap(modPath, fn.SourceMap)

			for _, arg := range fn.Args {
				if err := mod.namespaceTypeDef(ctx, modPath, arg.TypeDef); err != nil {
					return err
				}
				arg.SourceMap = mod.namespaceSourceMap(modPath, arg.SourceMap)
			}
		}
	case TypeDefKindEnum:
		enum := typeDef.AsEnum.Value

		// only namespace enums defined in this module
		mtype, ok, err := mod.Deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			enum.Values = mtype.TypeDef().AsEnum.Value.Values
		}
		if !ok {
			enum.Name = namespaceObject(enum.OriginalName, mod.Name(), mod.OriginalName)
			enum.SourceMap = mod.namespaceSourceMap(modPath, enum.SourceMap)
		}

		for _, value := range enum.Values {
			value.SourceMap = mod.namespaceSourceMap(modPath, value.SourceMap)
		}
	}
	return nil
}

func (mod *Module) namespaceSourceMap(modPath string, sourceMap *SourceMap) *SourceMap {
	if sourceMap == nil {
		return nil
	}

	if mod.Source.Self.Kind != ModuleSourceKindLocal {
		// TODO: handle remote git files
		return nil
	}

	sourceMap.Module = mod.Name()
	sourceMap.Filename = filepath.Join(modPath, sourceMap.Filename)
	return sourceMap
}

// modulePath gets the prefix for the file sourcemaps, so that the sourcemap is
// relative to the context directory
func (mod *Module) modulePath() string {
	return mod.Source.Self.SourceSubpath
}

/*
Mod is a module in loaded into the server's DAG of modules; it's the vertex type of the DAG.
It's an interface so we can abstract over user modules and core and treat them the same.
*/
type Mod interface {
	// Name gets the name of the module
	Name() string

	// View gets the name of the module's view of its underlying schema
	View() (string, bool)

	// Install modifies the provided server to install the contents of the
	// modules declared fields.
	Install(ctx context.Context, dag *dagql.Server) error

	// ModTypeFor returns the ModType for the given typedef based on this module's schema.
	// The returned type will have any namespacing already applied.
	// If checkDirectDeps is true, then its direct dependencies will also be checked.
	ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error)

	// TypeDefs gets the TypeDefs exposed by this module (not including dependencies)
	TypeDefs(ctx context.Context) ([]*TypeDef, error)

	// Source returns the ModuleSource for this module
	GetSource() *ModuleSource
}

// ClientGenerator is an interface that a module can implements to give client generation capabilities.
//
// The generated client is standalone and can be used in any project, even if no source code module is
// available.
type ClientGenerator interface {
	// RequiredClientGenerationFiles returns the list of files that are required from the host
	// to generate the client.
	RequiredClientGenerationFiles(ctx context.Context) (dagql.Array[dagql.String], error)

	// Generate client binding for the module and its dependencies at the given output directory.
	// The generated client will be placed in the same directory as the module source root dir
	// and contains bindings for all of the module's dependencies in addition to the
	// core API and the module itself if it got source code.
	//
	// It's up to that function to update the source root directory with additional
	// configurations if needed.
	// For example (executing go mod tidy, updating tsconfig.json etc...)
	//
	// The generated client should use the published library version of the SDK.
	// However, if the last parameter is set to true, a copy of the current SDK library
	// should be copied to test the generated client with latest changes.
	// NOTE: this should only be used for testing purposes.
	GenerateClient(context.Context, dagql.Instance[*ModuleSource], *ModDeps, string, bool) (dagql.Instance[*Directory], error)
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
	ClientGenerator

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
	if mod.Runtime != nil {
		dirDefs, err := mod.Runtime.PBDefinitions(ctx)
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

	if mod.SDKConfig != nil {
		cp.SDKConfig = mod.SDKConfig.Clone()
	}

	if mod.Deps != nil {
		cp.Deps = mod.Deps.Clone()
	}

	if mod.Runtime != nil {
		cp.Runtime = mod.Runtime.Clone()
	}

	cp.ObjectDefs = make([]*TypeDef, len(mod.ObjectDefs))
	for i, def := range mod.ObjectDefs {
		cp.ObjectDefs[i] = def.Clone()
	}

	cp.InterfaceDefs = make([]*TypeDef, len(mod.InterfaceDefs))
	for i, def := range mod.InterfaceDefs {
		cp.InterfaceDefs[i] = def.Clone()
	}

	cp.EnumDefs = make([]*TypeDef, len(mod.EnumDefs))
	for i, def := range mod.EnumDefs {
		cp.EnumDefs[i] = def.Clone()
	}

	if cp.SDKConfig != nil {
		cp.SDKConfig = cp.SDKConfig.Clone()
	}

	return &cp
}

func (mod Module) CloneWithoutDefs() *Module {
	cp := mod.Clone()

	cp.EnumDefs = []*TypeDef{}
	cp.ObjectDefs = []*TypeDef{}
	cp.InterfaceDefs = []*TypeDef{}

	return cp
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
		modPath := mod.modulePath()
		if err := mod.namespaceTypeDef(ctx, modPath, def); err != nil {
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
		modPath := mod.modulePath()
		if err := mod.namespaceTypeDef(ctx, modPath, def); err != nil {
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
		modPath := mod.modulePath()
		if err := mod.namespaceTypeDef(ctx, modPath, def); err != nil {
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
