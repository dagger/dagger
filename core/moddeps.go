package core

import (
	"context"
	"slices"

	"github.com/dagger/dagger/dagql"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"
	modMetaErrorPath  = "error"

	ModuleName = "daggercore"
)

var TypesToIgnoreForModuleIntrospection = []string{"Host"}

/*
ModDeps represents a set of dependencies for a module or for a caller depending on a
particular set of modules to be served.

Internally it delegates schema building, type definitions, and introspection
to a lazily-constructed ServedMods with default install options (all
constructors, no entrypoints).
*/
type ModDeps struct {
	root *Query
	Mods []Mod // TODO hide

	served *ServedMods // lazily built
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
	return d.ServedMods().Lookup(name)
}

// ServedMods returns the underlying ServedMods, building it lazily from
// the module list with default install options.
func (d *ModDeps) ServedMods() *ServedMods {
	if d.served == nil {
		d.served = NewServedMods(d.root)
		for _, mod := range d.Mods {
			d.served.Add(mod, InstallOpts{})
		}
	}
	return d.served
}

// The combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) Schema(ctx context.Context) (*dagql.Server, error) {
	return d.ServedMods().Schema(ctx)
}

// The introspection json for combined schema exposed by each mod in this set of dependencies, as a file.
// It is meant for consumption from modules, which have some APIs hidden from their codegen.
func (d *ModDeps) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes []string) (inst dagql.Result[*File], _ error) {
	return d.ServedMods().SchemaIntrospectionJSONFile(ctx, hiddenTypes)
}

// The introspection json for combined schema exposed by each mod in this set of dependencies, as a file.
// Some APIs are automatically hidden as they should not be exposed to modules.
func (d *ModDeps) SchemaIntrospectionJSONFileForModule(ctx context.Context) (inst dagql.Result[*File], _ error) {
	return d.ServedMods().SchemaIntrospectionJSONFileForModule(ctx)
}

// All the TypeDefs exposed by this set of dependencies.
func (d *ModDeps) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
	return d.ServedMods().TypeDefs(ctx, dag)
}

// Search the deps for the given type def, returning the ModType if found. This does not recurse
// to transitive dependencies; it only returns types directly exposed by the schema of the top-level
// deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	return d.ServedMods().ModTypeFor(ctx, typeDef)
}
