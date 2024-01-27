package core

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"
)

type Module struct {
	Query *Query

	// The source of the module
	Source dagql.Instance[*ModuleSource] `field:"true" name:"source" doc:"The source for the module."`

	// The name of the module
	NameField string `field:"true" name:"name" doc:"The name of the module"`

	// TODO:
	OriginalName string

	// The doc string of the module, if any
	Description string `field:"true" doc:"The doc string of the module, if any"`

	// The module's SDKConfig, as set in the module config file
	SDKConfig string `field:"true" name:"sdk" doc:"The SDK used by this module. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation."`

	// The module's root directory containing the config file for it and its source (possibly as a subdir). It includes any generated code or updated config files created after initial load.
	// TODO: rename to GeneratedSourceRootDirectory?
	GeneratedSourceDirectory dagql.Instance[*Directory]

	// The subpath of the GeneratedSourceDirectory that contains the actual source code of the module (which may be a subdir when dagger.json is a parent dir).
	// This is not always equal to Source.SourceSubpath in the case where the Source is a directory containing
	// dagger.json as a subdir.
	// E.g. if the module is in a git repo where dagger.json is at /foo/dagger.json and then module source code is at
	// /foo/mymod/, then GeneratedSourceSubpath will be "mymod".
	GeneratedSourceSubpath string

	// Dependencies as configured by the module
	DependencyConfig []*ModuleDependency `field:"true" doc:"The dependencies as configured by the module."`

	// The module's loaded dependencies, not yet initialized
	DependenciesField []dagql.Instance[*Module] `field:"true" name:"dependencies" doc:"Modules used by this module."`

	// Deps contains the module's dependency DAG.
	Deps *ModDeps

	DirectoryIncludeConfig []string
	DirectoryExcludeConfig []string

	// Runtime is the container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
	Runtime *Container

	// The following are populated while initializing the module

	// The module's objects
	ObjectDefs []*TypeDef `field:"true" name:"objects" doc:"Objects served by this module."`

	// The module's interfaces
	InterfaceDefs []*TypeDef `field:"true" name:"interfaces" doc:"Interfaces served by this module."`

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

func (mod *Module) WithName(ctx context.Context, name string) (*Module, error) {
	if mod.InstanceID != nil {
		return nil, fmt.Errorf("cannot update name on initialized module")
	}

	mod = mod.Clone()
	mod.NameField = name
	if mod.OriginalName == "" {
		mod.OriginalName = name
	}
	return mod, nil
}

func (mod *Module) WithSDK(ctx context.Context, sdk string) (*Module, error) {
	if mod.InstanceID != nil {
		return nil, fmt.Errorf("cannot update sdk on initialized module")
	}

	mod = mod.Clone()
	mod.SDKConfig = sdk
	return mod, nil
}

func (mod *Module) WithDependencies(
	ctx context.Context,
	srv *dagql.Server,
	dependencies []*ModuleDependency,
) (*Module, error) {
	if mod.InstanceID != nil {
		return nil, fmt.Errorf("cannot update dependencies on initialized module")
	}
	if mod.Source.Self == nil {
		return nil, fmt.Errorf("cannot update dependencies on module without source")
	}

	mod = mod.Clone()

	// resolve the dependency relative to this module's source
	var eg errgroup.Group
	for i, dep := range dependencies {
		if dep.Source.Self == nil {
			return nil, fmt.Errorf("dependency %d has no source", i)
		}
		i, dep := i, dep
		eg.Go(func() error {
			err := srv.Select(ctx, mod.Source, &dependencies[i].Source,
				dagql.Selector{
					Field: "resolveDependency",
					Args: []dagql.NamedInput{
						{Name: "dep", Value: dagql.NewID[*ModuleSource](dep.Source.ID())},
					},
				},
			)
			if err != nil {
				return fmt.Errorf("failed to resolve dependency module: %w", err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	// figure out the set of deps, keyed by their symbolic ref string, which de-dupes
	// equivalent sources at different versions, preferring the version provided
	// in the dependencies arg here
	depSet := make(map[string]*ModuleDependency)
	for _, dep := range mod.DependencyConfig {
		symbolic, err := dep.Source.Self.Symbolic()
		if err != nil {
			return nil, fmt.Errorf("failed to get symbolic source ref: %w", err)
		}
		depSet[symbolic] = dep
	}
	for _, newDep := range dependencies {
		symbolic, err := newDep.Source.Self.Symbolic()
		if err != nil {
			return nil, fmt.Errorf("failed to get symbolic source ref: %w", err)
		}
		depSet[symbolic] = newDep
	}

	mod.DependencyConfig = make([]*ModuleDependency, 0, len(depSet))
	for _, dep := range depSet {
		mod.DependencyConfig = append(mod.DependencyConfig, dep)
	}

	refStrs := make([]string, 0, len(mod.DependencyConfig))
	for _, dep := range mod.DependencyConfig {
		refStr, err := dep.Source.Self.RefString()
		if err != nil {
			return nil, fmt.Errorf("failed to get ref string for dependency: %w", err)
		}
		refStrs = append(refStrs, refStr)
	}
	sort.Slice(mod.DependencyConfig, func(i, j int) bool {
		return refStrs[i] < refStrs[j]
	})

	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	// TODO:
	for _, dep := range mod.DependencyConfig {
		refStr, err := dep.Source.Self.RefString()
		if err != nil {
			return nil, fmt.Errorf("failed to get ref string for dependency: %w", err)
		}
		slog.Debug("module dependency",
			"dep", refStr,
			"configName", dep.Name,
		)
	}

	mod.DependenciesField = make([]dagql.Instance[*Module], len(mod.DependencyConfig))
	eg = errgroup.Group{}
	for i, depCfg := range mod.DependencyConfig {
		i, depCfg := i, depCfg
		eg.Go(func() error {
			err := srv.Select(ctx, depCfg.Source, &mod.DependenciesField[i],
				dagql.Selector{
					Field: "asModule",
				},
				dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.NewString(depCfg.Name)},
					},
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
		if errors.Is(err, dagql.ErrCacheMapRecursiveCall) {
			err = fmt.Errorf("module %s has a circular dependency: %w", mod.NameField, err)
		}
		return nil, fmt.Errorf("failed to load updated dependencies: %w", err)
	}
	// fill in any missing names if needed
	for i, dep := range mod.DependencyConfig {
		if dep.Name == "" {
			dep.Name = mod.DependenciesField[i].Self.Name()
		}
	}

	// Check for loops by walking the DAG of deps, seeing if we ever encounter this mod.
	// Modules are identified here by their *Source's ID*, which is relatively stable and
	// unique to the module.
	// Note that this is ultimately best effort, subtle differences in IDs that don't actually
	// impact the final module will still result in us considering the two modules different.
	// E.g. if you initialize a module from a source directory that had a no-op file change
	// made to it, the ID used here will still change.
	selfID := mod.Source.ID().String()
	memo := make(map[string]struct{}) // set of id strings
	var visit func(dagql.Instance[*Module]) error
	visit = func(inst dagql.Instance[*Module]) error {
		instID := inst.Self.Source.ID().String()
		if instID == selfID {
			return fmt.Errorf("module %s has a circular dependency", mod.NameField)
		}

		if _, ok := memo[instID]; ok {
			return nil
		}
		memo[instID] = struct{}{}

		for _, dep := range inst.Self.DependenciesField {
			if err := visit(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, dep := range mod.DependenciesField {
		if err := visit(dep); err != nil {
			return nil, err
		}
	}

	mod.Deps = NewModDeps(mod.Query, mod.Dependencies()).
		Append(mod.Query.DefaultDeps.Mods...)

	return mod, nil
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
	Codegen(context.Context, *Module, dagql.Instance[*ModuleSource]) (*GeneratedCode, error)

	/* Runtime returns a container that is used to execute module code at runtime in the Dagger engine.

	The provided Module is not fully initialized; the Runtime field will not be set yet.
	*/
	Runtime(context.Context, *Module, dagql.Instance[*ModuleSource]) (*Container, error)
}

var _ HasPBDefinitions = (*Module)(nil)

func (mod *Module) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if mod.GeneratedSourceDirectory.Self != nil {
		dirDefs, err := mod.GeneratedSourceDirectory.Self.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		defs = append(defs, dirDefs...)
	}
	return defs, nil
}

func (mod Module) Clone() *Module {
	cp := mod

	if mod.Source.Self != nil {
		cp.Source.Self = mod.Source.Self.Clone()
	}

	if mod.GeneratedSourceDirectory.Self != nil {
		cp.GeneratedSourceDirectory.Self = mod.GeneratedSourceDirectory.Self.Clone()
	}

	cp.DependencyConfig = make([]*ModuleDependency, len(mod.DependencyConfig))
	for i, dep := range mod.DependencyConfig {
		cp.DependencyConfig[i] = dep.Clone()
	}

	cp.DependenciesField = make([]dagql.Instance[*Module], len(mod.DependenciesField))
	for i, dep := range mod.DependenciesField {
		cp.DependenciesField[i].Self = dep.Self.Clone()
	}

	if len(mod.DirectoryIncludeConfig) > 0 {
		cp.DirectoryIncludeConfig = make([]string, len(mod.DirectoryIncludeConfig))
		copy(cp.DirectoryIncludeConfig, mod.DirectoryIncludeConfig)
	}
	if len(mod.DirectoryExcludeConfig) > 0 {
		cp.DirectoryExcludeConfig = make([]string, len(mod.DirectoryExcludeConfig))
		copy(cp.DirectoryExcludeConfig, mod.DirectoryExcludeConfig)
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
