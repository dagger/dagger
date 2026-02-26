package core

import (
	"context"
	"fmt"
	"slices"
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

var (
	TypesToIgnoreForModuleIntrospection = []string{"Host"}
)

/*
ModDeps represents a set of dependencies for a module or for a caller depending on a
particular set of modules to be served.
*/
type ModDeps struct {
	root *Query
	Mods []Mod // TODO hide

	// mains tracks module names whose constructors should be installed
	// on the Query root. Modules not in this set are installed for type
	// resolution only (e.g. transitive dependencies whose types may be
	// returned through interfaces).
	mains map[string]bool

	// should not be read directly, call Schema and SchemaIntrospectionJSON instead
	lazilyLoadedSchema         *dagql.Server
	lazilyLoadedSchemaJSONFile dagql.Result[*File]
	loadSchemaErr              error
	loadSchemaLock             sync.Mutex
}

func NewModDeps(root *Query, mods []Mod) *ModDeps {
	return &ModDeps{
		root: root,
		Mods: slices.Clone(mods),
	}
}

func (d *ModDeps) cloneMains() map[string]bool {
	if len(d.mains) == 0 {
		return nil
	}
	cp := make(map[string]bool, len(d.mains))
	for k, v := range d.mains {
		cp[k] = v
	}
	return cp
}

func (d *ModDeps) Clone() *ModDeps {
	cp := NewModDeps(d.root, slices.Clone(d.Mods))
	cp.mains = d.cloneMains()
	return cp
}

func (d *ModDeps) Prepend(mods ...Mod) *ModDeps {
	cp := d.Clone()
	cp.Mods = append(slices.Clone(mods), cp.Mods...)
	return cp
}

func (d *ModDeps) Append(mods ...Mod) *ModDeps {
	cp := d.Clone()
	cp.Mods = append(cp.Mods, mods...)
	return cp
}

// SetMain marks a module name as a "main" module whose constructor should
// be installed on the Query root. Modules not marked are installed for
// type resolution only.
func (d *ModDeps) SetMain(name string) *ModDeps {
	cp := d.Clone()
	if cp.mains == nil {
		cp.mains = make(map[string]bool)
	}
	cp.mains[name] = true
	return cp
}

// IsMain reports whether the named module should have its constructor
// installed on the Query root. If no modules have been explicitly marked
// as main (via SetMain), all modules are treated as main by default.
func (d *ModDeps) IsMain(name string) bool {
	if len(d.mains) == 0 {
		return true
	}
	return d.mains[name]
}

func (d *ModDeps) LookupDep(name string) (Mod, bool) {
	for _, mod := range d.Mods {
		if mod.Name() == name {
			return mod, true
		}
	}

	return nil, false
}

// The combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) Schema(ctx context.Context) (*dagql.Server, error) {
	schema, _, err := d.lazilyLoadSchema(ctx, []string{})
	if err != nil {
		return nil, err
	}
	return schema, nil
}

// The introspection json for combined schema exposed by each mod in this set of dependencies, as a file.
// It is meant for consumption from modules, which have some APIs hidden from their codegen.
func (d *ModDeps) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes []string) (inst dagql.Result[*File], _ error) {
	_, schemaJSONFile, err := d.lazilyLoadSchema(ctx, hiddenTypes)
	if err != nil {
		return inst, err
	}
	return schemaJSONFile, nil
}

// The introspection json for combined schema exposed by each mod in this set of dependencies, as a file.
// Some APIs are automatically hidden as they should not be exposed to modules.
func (d *ModDeps) SchemaIntrospectionJSONFileForModule(ctx context.Context) (inst dagql.Result[*File], _ error) {
	return d.SchemaIntrospectionJSONFile(ctx, TypesToIgnoreForModuleIntrospection)
}

// All the TypeDefs exposed by this set of dependencies
func (d *ModDeps) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
	var typeDefs []*TypeDef
	for _, mod := range d.Mods {
		modTypeDefs, err := mod.TypeDefs(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("failed to get objects from mod %q: %w", mod.Name(), err)
		}
		typeDefs = append(typeDefs, modTypeDefs...)
	}
	return typeDefs, nil
}

func (d *ModDeps) lazilyLoadSchema(ctx context.Context, hiddenTypes []string) (
	loadedSchema *dagql.Server,
	loadedSchemaJSONFile dagql.Result[*File],
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

	dagqlCache, err := d.root.Cache(ctx)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get cache: %w", err)
	}
	dag := dagql.NewServer(d.root, dagqlCache)
	for _, mod := range d.Mods {
		if version, ok := mod.View(); ok {
			dag.View = version
			break
		}
	}

	dag.Around(AroundFunc)

	dagintro.Install[*Query](dag)

	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, mod := range d.Mods {
		err := mod.Install(ctx, dag, InstallOpts{
			SkipConstructor: !d.IsMain(mod.Name()),
		})
		if err != nil {
			return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}

		// TODO support core interfaces types
		if userMod, ok := mod.(*Module); ok {
			defs, err := mod.TypeDefs(ctx, dag)
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
	// fmt.Fprintf(os.Stderr, "🧪 objects: %d, interfaces: %d\n", len(objects), len(ifaces))
	for _, objType := range objects {
		// fmt.Fprintf(os.Stderr, "🔍 object=%s\n", objType.typeDef.Name)
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
					Name:           asIfaceFieldName,
					Description:    fmt.Sprintf("Converts this %s to a %s.", obj.Name, iface.Name),
					Type:           &InterfaceAnnotatedValue{TypeDef: iface},
					Module:         ifaceType.mod.IDModule(),
					GetCacheConfig: ifaceType.mod.CacheConfigForCall,
				},
				func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
					inst, ok := dagql.UnwrapAs[*ModuleObject](self)
					if !ok {
						return nil, fmt.Errorf("expected %T to be a ModuleObject", self)
					}
					return dagql.NewObjectResultForCurrentID(ctx, dag, &InterfaceAnnotatedValue{
						TypeDef:        iface,
						Fields:         inst.Fields,
						UnderlyingType: objType,
						IfaceType:      ifaceType,
					})
				},
			)
		}
	}

	if err := dag.Select(ctx, dag.Root(), &loadedSchemaJSONFile,
		dagql.Selector{
			Field: "__schemaJSONFile",
			Args: []dagql.NamedInput{
				{
					Name:  "hiddenTypes",
					Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(hiddenTypes...)),
				},
			},
		},
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
