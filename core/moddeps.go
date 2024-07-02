package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/dagql"
	dagintro "github.com/dagger/dagger/dagql/introspection"
	"github.com/moby/buildkit/util/compression"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"

	ModuleName = "daggercore"
)

// We don't expose these types to modules SDK codegen, but
// we still want their graphql schemas to be available for
// internal usage. So we use this list to scrub them from
// the introspection JSON that module SDKs use for codegen.
var typesHiddenFromModuleSDKs = []dagql.Typed{
	&Host{},
}

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
		Mods: mods,
	}
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

func schemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
	data, err := dag.Query(ctx, introspection.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return json.RawMessage(jsonBytes), nil
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
			objType := objType
			ifaceType := ifaceType
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
			)
		}
	}

	schemaJSON, err := schemaIntrospectionJSON(ctx, dag)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get schema introspection JSON: %w", err)
	}
	var introspection introspection.Response
	if err := json.Unmarshal([]byte(schemaJSON), &introspection); err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}
	for _, typed := range typesHiddenFromModuleSDKs {
		introspection.Schema.ScrubType(typed.Type().Name())
		introspection.Schema.ScrubType(dagql.IDTypeNameFor(typed))
	}
	moduleSchemaJSON, err := json.Marshal(introspection)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to marshal introspection JSON: %w", err)
	}

	const schemaJSONFilename = "schema.json"

	bk, err := d.root.Buildkit(ctx)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	_, schemaJSONDesc, err := bk.BytesToBlob(ctx,
		schemaJSONFilename,
		0644,
		moduleSchemaJSON,
		// don't bother compressing these in the content store, they aren't quite *that* big that we
		// need to save on disk space in exchange for CPU time
		compression.Uncompressed,
	)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to create blob for introspection JSON: %w", err)
	}
	dirInst, err := LoadBlob(ctx, dag, schemaJSONDesc)
	if err != nil {
		return nil, loadedSchemaJSONFile, fmt.Errorf("failed to load introspection JSON blob: %w", err)
	}
	if err := dag.Select(ctx, dirInst, &loadedSchemaJSONFile,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(schemaJSONFilename)},
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
