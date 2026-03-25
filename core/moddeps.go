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
// Note: ModDeps has no entrypoint knowledge, so all module-provided Query
// fields are treated as constructors. Use ServedMods.TypeDefs for
// entrypoint-aware merging.
func (d *ModDeps) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
	return d.ServedMods().TypeDefs(ctx, dag)
}

// Search the deps for the given type def, returning the ModType if found. This does not recurse
// to transitive dependencies; it only returns types directly exposed by the schema of the top-level
// deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	return d.ServedMods().ModTypeFor(ctx, typeDef)
}

// mergeModuleQueryFields finds the Query TypeDef and adds any module-provided
// fields (constructors and entrypoint proxy methods) using the function
// metadata from the source module's own TypeDefs.
//
// entrypointMods is the set of module names that are installed as entrypoints.
// Only entrypoint modules can have proxy methods on Query; all other
// module-provided Query fields are constructors.
func mergeModuleQueryFields(typeDefs []*TypeDef, dag *dagql.Server, entrypointMods map[string]bool) []*TypeDef {
	queryObjType := dag.Root().ObjectType()

	// Find the Query TypeDef and build a lookup of module main objects by
	// source module name. IsMainObject is set by Module.TypeDefs() so we
	// don't need name-matching heuristics here.
	var queryTypeDef *ObjectTypeDef
	modMainObjects := map[string]*ObjectTypeDef{}
	for _, td := range typeDefs {
		if td.Kind == TypeDefKindObject && td.AsObject.Valid {
			obj := td.AsObject.Value
			if obj.Name == "Query" {
				queryTypeDef = obj
			}
			if obj.IsMainObject {
				modMainObjects[obj.SourceModuleName] = obj
			}
		}
	}
	if queryTypeDef == nil {
		return typeDefs
	}

	// Collect existing Query function names so we don't add duplicates.
	existingFns := map[string]bool{}
	for _, fn := range queryTypeDef.Functions {
		existingFns[fn.Name] = true
	}

	// First pass: add entrypoint proxy methods directly from the main
	// object's type definition. We derive these from the module's own
	// TypeDefs rather than from dagql FieldSpecs because a dep constructor
	// with the same name may have been the last-registered FieldSpec before
	// the entrypoint was installed. By adding proxies first, the CLI sees
	// them as module functions.
	for modName := range entrypointMods {
		mainObj, ok := modMainObjects[modName]
		if !ok {
			continue
		}
		for _, fn := range mainObj.Functions {
			fieldName := gqlFieldName(fn.Name)
			if existingFns[fieldName] {
				continue
			}
			// Verify the field actually exists on Query.
			if _, found := queryObjType.FieldSpec(fieldName, dag.View); !found {
				continue
			}
			proxied := fn.Clone()
			proxied.SourceModuleName = modName
			queryTypeDef.Functions = append(queryTypeDef.Functions, proxied)
			existingFns[fieldName] = true
		}
		// Also add the "with" constructor if present.
		if !existingFns["with"] {
			if _, found := queryObjType.FieldSpec("with", dag.View); found {
				withFn := &Function{
					Name:             "with",
					Description:      mainObj.Description,
					SourceModuleName: modName,
					ReturnType: &TypeDef{
						Kind: TypeDefKindObject,
						AsObject: dagql.NonNull(&ObjectTypeDef{
							Name: "Query",
						}),
					},
				}
				if mainObj.Constructor.Valid {
					withFn.Args = mainObj.Constructor.Value.Args
				}
				queryTypeDef.Functions = append(queryTypeDef.Functions, withFn)
				existingFns["with"] = true
			}
		}
	}

	// Second pass: add constructors for non-entrypoint modules. We derive
	// these from modMainObjects rather than FieldSpecs because an entrypoint
	// proxy can shadow a dep constructor of the same name in FieldSpecs.
	// Both need to appear in the TypeDef so that initializeModule can find
	// each module's constructor by SourceModuleName.
	for modName, mainObj := range modMainObjects {
		if entrypointMods[modName] {
			continue
		}
		constructorName := gqlFieldName(modName)
		// The constructor field exists on the dagql server even if an
		// entrypoint proxy with the same name was installed later
		// (dagql appends, so both exist; FieldSpec returns last-wins
		// but the field is still callable).
		if _, found := queryObjType.FieldSpec(constructorName, dag.View); !found {
			continue
		}
		fn := constructorFunctionFromMainObject(mainObj, constructorName, modName)
		queryTypeDef.Functions = append(queryTypeDef.Functions, fn)
	}

	return typeDefs
}

// constructorFunctionFromMainObject creates a Function TypeDef for a module
// constructor on Query. The constructor returns the module's main object.
func constructorFunctionFromMainObject(mainObj *ObjectTypeDef, name, modName string) *Function {
	fn := &Function{
		Name:             name,
		Description:      mainObj.Description,
		SourceModuleName: modName,
		ReturnType: &TypeDef{
			Kind: TypeDefKindObject,
			AsObject: dagql.NonNull(&ObjectTypeDef{
				Name: mainObj.Name,
			}),
		},
	}
	// Constructor args come from the module's explicit constructor if defined.
	if mainObj.Constructor.Valid {
		fn.Args = mainObj.Constructor.Value.Args
	}
	return fn
}

// findFunctionOnObject looks up a function or field by its GraphQL field name
// on an object. Fields are converted to a Function representation so that
// callers can treat both uniformly.
func findFunctionOnObject(obj *ObjectTypeDef, fieldName string) *Function {
	for _, fn := range obj.Functions {
		if gqlFieldName(fn.Name) == fieldName {
			return fn
		}
	}
	for _, f := range obj.Fields {
		if gqlFieldName(f.Name) == fieldName {
			return &Function{
				Name:        f.Name,
				Description: f.Description,
				ReturnType:  f.TypeDef,
			}
		}
	}
	return nil
}
