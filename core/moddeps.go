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

	// Merge module-provided Query fields into the Query TypeDef.
	//
	// CoreMod.TypeDefs skips Query fields provided by modules to avoid a lossy
	// introspection round-trip (which loses interface return types, directives,
	// etc.). Instead, we add those functions here using the module's own
	// TypeDefs, which have the correct metadata.
	typeDefs = mergeModuleQueryFields(typeDefs, dag)

	return typeDefs, nil
}

// mergeModuleQueryFields finds the Query TypeDef and adds any module-provided
// fields (constructors and entrypoint proxy methods) using the function
// metadata from the source module's own TypeDefs.
func mergeModuleQueryFields(typeDefs []*TypeDef, dag *dagql.Server) []*TypeDef {
	queryObjType := dag.Root().ObjectType()

	// Find the Query TypeDef and build a lookup of module main objects by
	// source module name. Only the primary object (whose name matches the
	// module name) is stored — secondary objects from the same module
	// (e.g. TestObj from module "test") must not overwrite it.
	var queryTypeDef *ObjectTypeDef
	modMainObjects := map[string]*ObjectTypeDef{}
	for _, td := range typeDefs {
		if td.Kind == TypeDefKindObject && td.AsObject.Valid {
			obj := td.AsObject.Value
			if obj.Name == "Query" {
				queryTypeDef = obj
			}
			if obj.SourceModuleName != "" && gqlObjectName(obj.SourceModuleName) == obj.Name {
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

	// Enumerate module-provided Query fields directly from the dagql type.
	for _, spec := range queryObjType.FieldSpecs(dag.View) {
		if existingFns[spec.Name] || spec.Module == nil {
			continue
		}

		modName := spec.Module.Name()
		mainObj, ok := modMainObjects[modName]
		if !ok {
			continue
		}

		// Check if this field is a proxy for one of the main object's
		// methods (entrypoint proxy). If so, use the method's function
		// definition directly — it has the correct return type, directives,
		// etc.
		if fn := findFunctionOnObject(mainObj, spec.Name); fn != nil {
			proxied := fn.Clone()
			proxied.SourceModuleName = modName
			queryTypeDef.Functions = append(queryTypeDef.Functions, proxied)
			continue
		}

		// Otherwise this is the constructor. Synthesize a function that
		// returns the main object type, using args from the module's
		// explicit constructor if one was defined.
		fn := constructorFunctionFromMainObject(mainObj, spec.Name, modName)
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
