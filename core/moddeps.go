package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	dagintro "github.com/dagger/dagger/dagql/introspection"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"
	modMetaErrorPath  = "error"

	ModuleName = "daggercore"
)

/*
ModDeps represents a set of dependencies for a module or for a caller depending on a
particular set of modules to be served.
*/
type ModDeps struct {
	root *Query
	Mods []Mod // TODO hide

	// should not be read directly, call Schema and SchemaIntrospectionJSON instead
	lazilyLoadedSchema         *dagql.Server
	lazilyLoadedSchemaJSONFile dagql.Instance[*File]
	loadSchemaErr              error
	loadSchemaLock             sync.Mutex
}

func NewModDeps(root *Query, mods []Mod) *ModDeps {
	return &ModDeps{
		root: root,
		Mods: append([]Mod{}, mods...),
	}
}

func (d *ModDeps) Clone() *ModDeps {
	return NewModDeps(d.root, append([]Mod{}, d.Mods...))
}

func (d *ModDeps) Prepend(mods ...Mod) *ModDeps {
	deps := append([]Mod{}, mods...)
	deps = append(deps, d.Mods...)
	return NewModDeps(d.root, deps)
}

func (d *ModDeps) Append(mods ...Mod) *ModDeps {
	deps := append([]Mod{}, d.Mods...)
	deps = append(deps, mods...)
	return NewModDeps(d.root, deps)
}

func (d *ModDeps) LookupDep(name string) bool {
	for _, mod := range d.Mods {
		if mod.Name() == name {
			return true
		}
	}

	return false
}

// The combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) Schema(ctx context.Context) (*dagql.Server, error) {
	schema, _, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

// The introspection json for combined schema exposed by each mod in this set of dependencies, as a file.
// It is meant for consumption from modules, which have some APIs hidden from their codegen.
func (d *ModDeps) SchemaIntrospectionJSONFile(ctx context.Context) (inst dagql.Instance[*File], _ error) {
	_, schemaJSONFile, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return inst, err
	}
	return schemaJSONFile, nil
}

// All the TypeDefs exposed by this set of dependencies
func (d *ModDeps) TypeDefs(ctx context.Context) ([]*TypeDef, error) {
	var typeDefs []*TypeDef
	for _, mod := range d.Mods {
		modTypeDefs, err := mod.TypeDefs(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get objects from mod %q: %w", mod.Name(), err)
		}
		typeDefs = append(typeDefs, modTypeDefs...)
	}
	return typeDefs, nil
}

func (d *ModDeps) lazilyLoadSchema(ctx context.Context) (
	loadedSchema *dagql.Server,
	loadedSchemaJSONFile dagql.Instance[*File],
	rerr error,
) {
	d.loadSchemaLock.Lock()
	defer d.loadSchemaLock.Unlock()
	if d.lazilyLoadedSchema != nil {
		return d.lazilyLoadedSchema, d.lazilyLoadedSchemaJSONFile, nil
	}
	if d.loadSchemaErr != nil {
		return nil, loadedSchemaJSONFile, d.loadSchemaErr
	}
	defer func() {
		d.lazilyLoadedSchema = loadedSchema
		d.lazilyLoadedSchemaJSONFile = loadedSchemaJSONFile
		d.loadSchemaErr = rerr
	}()

	dag := dagql.NewServer[*Query](d.root)
	for _, mod := range d.Mods {
		if version, ok := mod.View(); ok {
			dag.View = version
			break
		}
	}

	dag.Around(AroundFunc)

	// share the same cache session-wide
	var err error
	dag.Cache, err = d.root.Cache(ctx)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get cache: %w", err)
	}

	dagintro.Install[*Query](dag)

	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, mod := range d.Mods {
		err := mod.Install(ctx, dag)
		if err != nil {
			return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}

		// TODO support core interfaces types
		if userMod, ok := mod.(*Module); ok {
			defs, err := mod.TypeDefs(ctx)
			if err != nil {
				return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get type defs for module %q: %w", mod.Name(), err)
			}
			for _, def := range defs {
				switch def.Kind {
				case TypeDefKindObject:
					objects = append(objects, &ModuleObjectType{
						typeDef: def.AsObject.Value,
						mod:     userMod,
					})
				case TypeDefKindInterface:
					ifaces = append(ifaces, &InterfaceType{
						typeDef: def.AsInterface.Value,
						mod:     userMod,
					})
				}
			}
		}
	}

	// add any extensions to objects for the interfaces they implement (if any)
	for _, objType := range objects {
		obj := objType.typeDef
		class, found := dag.ObjectType(obj.Name)
		if !found {
			return nil, loadedSchemaJSONFile, fmt.Errorf("failed to find object %q in schema", obj.Name)
		}
		for _, ifaceType := range ifaces {
			iface := ifaceType.typeDef
			if !obj.IsSubtypeOf(iface) {
				continue
			}
			asIfaceFieldName := gqlFieldName(fmt.Sprintf("as%s", iface.Name))
			class.Extend(
				dagql.FieldSpec{
					Name:        asIfaceFieldName,
					Description: fmt.Sprintf("Converts this %s to a %s.", obj.Name, iface.Name),
					Type:        &InterfaceAnnotatedValue{TypeDef: iface},
					Module:      ifaceType.mod.IDModule(),
				},
				func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
					inst, ok := self.(dagql.Instance[*ModuleObject])
					if !ok {
						return nil, fmt.Errorf("expected %T to be a ModuleObject", self)
					}
					return &InterfaceAnnotatedValue{
						TypeDef:        iface,
						Fields:         inst.Self.Fields,
						UnderlyingType: objType,
						IfaceType:      ifaceType,
					}, nil
				},
				dagql.CacheSpec{},
			)
		}
	}

	if err := dag.Select(ctx, dag.Root(), &loadedSchemaJSONFile,
		dagql.Selector{Field: "__schemaJSONFile"},
	); err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to select introspection JSON file: %w", err)
	}

	return dag, loadedSchemaJSONFile, nil
}

// Search the deps for the given type def, returning the ModType if found. This does not recurse
// to transitive dependencies; it only returns types directly exposed by the schema of the top-level
// deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	for _, mod := range d.Mods {
		modType, ok, err := mod.ModTypeFor(ctx, typeDef, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get type from mod %q: %w", mod.Name(), err)
		}
		if !ok {
			continue
		}
		return modType, true, nil
	}
	return nil, false, nil
}
