package core

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql"
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

func (d *ModDeps) Clone() *ModDeps {
	return NewModDeps(d.root, slices.Clone(d.Mods))
}

func (d *ModDeps) Prepend(mods ...Mod) *ModDeps {
	deps := slices.Clone(mods)
	deps = append(deps, d.Mods...)
	return NewModDeps(d.root, deps)
}

func (d *ModDeps) Append(mods ...Mod) *ModDeps {
	deps := slices.Clone(d.Mods)
	deps = append(deps, mods...)
	return NewModDeps(d.root, deps)
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

	// All modules in a ModDeps get full installation (with constructors).
	mods := make([]modInstall, len(d.Mods))
	for i, mod := range d.Mods {
		mods[i] = modInstall{mod: mod}
	}

	dag, schemaJSONFile, err := buildSchema(ctx, d.root, mods, hiddenTypes)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}
	return dag, schemaJSONFile, nil
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
