package schema

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/formatter"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"

	coreModuleName = "daggercore"
)

/*
Mod is a module in loaded into the server's DAG of modules; it's the vertex type of the DAG.
It's an interface so we can abstract over user modules and core and treat them the same.
*/
type Mod interface {
	// The name of the module
	Name() string

	// The digest of the module itself plus the recursive digests of the DAG it depends on
	DagDigest() digest.Digest

	// The direct dependencies of this module
	Dependencies() []Mod

	// The schema+resolvers exposed by this module (does not include dependencies)
	Schema(context.Context) ([]SchemaResolvers, error)

	// The introspection json for this module's schema
	SchemaIntrospectionJSON(context.Context) (string, error)

	// ModTypeFor returns the ModType for the given typedef based on this module's schema.
	// The returned type will have any namespacing already applied.
	// If checkDirectDeps is true, then its direct dependencies will also be checked.
	ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error)
}

// CoreMod is a special implementation of Mod for our core API, which is not *technically* a true module yet
// but can be treated as one in terms of dependencies. It has no dependencies itself and is currently an
// implicit dependency of every user module.
type CoreMod struct {
	compiledSchema    *CompiledSchema
	introspectionJSON string
}

var _ Mod = (*CoreMod)(nil)

func (m *CoreMod) Name() string {
	return coreModuleName
}

func (m *CoreMod) DagDigest() digest.Digest {
	// core is always a leaf, so we just return a static digest
	return digest.FromString(coreModuleName)
}

func (m *CoreMod) Dependencies() []Mod {
	return nil
}

func (m *CoreMod) Schema(_ context.Context) ([]SchemaResolvers, error) {
	return []SchemaResolvers{m.compiledSchema.SchemaResolvers}, nil
}

func (m *CoreMod) SchemaIntrospectionJSON(_ context.Context) (string, error) {
	return m.introspectionJSON, nil
}

func (m *CoreMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return &PrimitiveType{}, true, nil

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.ElementTypeDef, checkDirectDeps)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &ListType{underlying: underlyingType}, true, nil

	case core.TypeDefKindObject:
		typeName := gqlObjectName(typeDef.AsObject.Name)
		resolver, ok := m.compiledSchema.Resolvers()[typeName]
		if !ok {
			return nil, false, nil
		}
		idableResolver, ok := resolver.(IDableObjectResolver)
		if !ok {
			return nil, false, nil
		}
		return &CoreModObject{coreMod: m, resolver: idableResolver}, true, nil

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}
}

/*
A user defined module loaded into this server's DAG.
*/
type UserMod struct {
	api      *APIServer
	metadata *core.Module
	deps     *ModDeps
	sdk      SDK

	dagDigest digest.Digest

	// should not be read directly, call m.Objects() instead
	lazilyLoadedObjects []*UserModObject
	loadObjectsErr      error
	loadObjectsLock     sync.Mutex
}

var _ Mod = (*UserMod)(nil)

func newUserMod(api *APIServer, modMeta *core.Module, deps *ModDeps, sdk SDK) (*UserMod, error) {
	selfDigest, err := modMeta.BaseDigest()
	if err != nil {
		return nil, fmt.Errorf("failed to get module digest: %w", err)
	}
	dagDigest := digest.FromString(selfDigest.String() + " " + deps.DagDigest().String())

	m := &UserMod{
		api:       api,
		metadata:  modMeta,
		deps:      deps,
		sdk:       sdk,
		dagDigest: dagDigest,
	}
	return m, nil
}

func (m *UserMod) Name() string {
	return m.metadata.Name
}

func (m *UserMod) DagDigest() digest.Digest {
	return m.dagDigest
}

func (m *UserMod) Dependencies() []Mod {
	return m.deps.mods
}

func (m *UserMod) Codegen(ctx context.Context) (*core.GeneratedCode, error) {
	return m.sdk.Codegen(ctx, m)
}

func (m *UserMod) Runtime(ctx context.Context) (*core.Container, error) {
	return m.sdk.Runtime(ctx, m)
}

// The objects defined by this module, with namespacing applied
func (m *UserMod) Objects(ctx context.Context) (loadedObjects []*UserModObject, rerr error) {
	m.loadObjectsLock.Lock()
	defer m.loadObjectsLock.Unlock()
	if len(m.lazilyLoadedObjects) > 0 {
		return m.lazilyLoadedObjects, nil
	}
	if m.loadObjectsErr != nil {
		return nil, m.loadObjectsErr
	}

	defer func() {
		m.lazilyLoadedObjects = loadedObjects
		m.loadObjectsErr = rerr
	}()

	runtime, err := m.Runtime(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module runtime: %w", err)
	}

	// construct a special function with no object or function name, which tells the SDK to return the module's definition
	// (in terms of objects, fields and functions)
	getModDefFn, err := newModFunction(ctx, m, nil, runtime, core.NewFunction("", &core.TypeDef{
		Kind:     core.TypeDefKindObject,
		AsObject: core.NewObjectTypeDef("Module", ""),
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to create module definition function for module %q: %w", m.Name(), err)
	}
	result, err := getModDefFn.Call(ctx, &CallOpts{Cache: true})
	if err != nil {
		return nil, fmt.Errorf("failed to call module %q to get functions: %w", m.Name(), err)
	}

	modMeta, ok := result.(*core.Module)
	if !ok {
		return nil, fmt.Errorf("expected ModuleMetadata result, got %T", result)
	}

	objs := make([]*UserModObject, 0, len(modMeta.Objects))
	for _, objTypeDef := range modMeta.Objects {
		if err := m.validateTypeDef(ctx, objTypeDef); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}

		if err := m.namespaceTypeDef(ctx, objTypeDef); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}

		obj, err := newModObject(ctx, m, objTypeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to create object: %w", err)
		}
		objs = append(objs, obj)
	}
	return objs, nil
}

func (m *UserMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return &PrimitiveType{}, true, nil

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.ElementTypeDef, checkDirectDeps)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &ListType{underlying: underlyingType}, true, nil

	case core.TypeDefKindObject:
		if checkDirectDeps {
			// check to see if this is from a *direct* dependency
			depType, ok, err := m.deps.ModTypeFor(ctx, typeDef)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get type from dependency: %w", err)
			}
			if ok {
				return depType, true, nil
			}
		}

		// otherwise it must be from this module
		objs, err := m.Objects(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, obj := range objs {
			if obj.typeDef.AsObject.Name == typeDef.AsObject.Name {
				return obj, true, nil
			}
		}
		return nil, false, nil

	default:
		return nil, false, fmt.Errorf("unexpected type def kind %s", typeDef.Kind)
	}
}

func (m *UserMod) MainModuleObject(ctx context.Context) (*UserModObject, error) {
	objs, err := m.Objects(ctx)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		if obj.typeDef.AsObject.Name == gqlObjectName(m.metadata.Name) {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("failed to find main module object %q", m.metadata.Name)
}

func (m *UserMod) Schema(ctx context.Context) ([]SchemaResolvers, error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("module", m.Name()))
	bklog.G(ctx).Debug("getting module schema")

	objs, err := m.Objects(ctx)
	if err != nil {
		return nil, err
	}

	schemas := make([]SchemaResolvers, 0, len(objs))
	for _, obj := range objs {
		objSchemaDoc, objResolvers, err := obj.Schema(ctx)
		if err != nil {
			return nil, err
		}
		buf := &bytes.Buffer{}
		formatter.NewFormatter(buf).FormatSchemaDocument(objSchemaDoc)
		typeSchemaStr := buf.String()

		schema := StaticSchema(StaticSchemaParams{
			Name:      fmt.Sprintf("%s.%s", m.metadata.Name, obj.typeDef.AsObject.Name),
			Schema:    typeSchemaStr,
			Resolvers: objResolvers,
		})
		schemas = append(schemas, schema)
	}

	return schemas, nil
}

func (m *UserMod) SchemaIntrospectionJSON(ctx context.Context) (string, error) {
	return m.deps.SchemaIntrospectionJSON(ctx)
}

// verify the typedef is has no reserved names
func (m *UserMod) validateTypeDef(ctx context.Context, typeDef *core.TypeDef) error {
	switch typeDef.Kind {
	case core.TypeDefKindList:
		return m.validateTypeDef(ctx, typeDef.AsList.ElementTypeDef)
	case core.TypeDefKindObject:
		obj := typeDef.AsObject

		// check whether this is a pre-existing object from core or another module
		modType, ok, err := m.deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod.DagDigest() != m.DagDigest() {
				// already validated, skip
				return nil
			}
		}

		for _, field := range obj.Fields {
			if gqlFieldName(field.Name) == "id" {
				return fmt.Errorf("cannot define field with reserved name %q on object %q", field.Name, obj.Name)
			}
			if err := m.validateTypeDef(ctx, field.TypeDef); err != nil {
				return err
			}
		}

		for _, fn := range obj.Functions {
			if gqlFieldName(fn.Name) == "id" {
				return fmt.Errorf("cannot define function with reserved name %q on object %q", fn.Name, obj.Name)
			}
			if err := m.validateTypeDef(ctx, fn.ReturnType); err != nil {
				return err
			}

			for _, arg := range fn.Args {
				if gqlArgName(arg.Name) == "id" {
					return fmt.Errorf("cannot define argument with reserved name %q on function %q", arg.Name, fn.Name)
				}
				if err := m.validateTypeDef(ctx, arg.TypeDef); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// prefix the given typedef (and any recursively referenced typedefs) with this module's name for any objects
func (m *UserMod) namespaceTypeDef(ctx context.Context, typeDef *core.TypeDef) error {
	switch typeDef.Kind {
	case core.TypeDefKindList:
		if err := m.namespaceTypeDef(ctx, typeDef.AsList.ElementTypeDef); err != nil {
			return err
		}
	case core.TypeDefKindObject:
		obj := typeDef.AsObject

		// only namespace objects defined in this module
		_, ok, err := m.deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if !ok {
			obj.Name = namespaceObject(obj.Name, m.metadata.Name)
		}

		for _, field := range obj.Fields {
			if err := m.namespaceTypeDef(ctx, field.TypeDef); err != nil {
				return err
			}
		}

		for _, fn := range obj.Functions {
			if err := m.namespaceTypeDef(ctx, fn.ReturnType); err != nil {
				return err
			}

			for _, arg := range fn.Args {
				if err := m.namespaceTypeDef(ctx, arg.TypeDef); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

/*
ModDeps represents a set of dependencies for a module or for a caller depending on a
particular set of modules to be served.
*/
type ModDeps struct {
	api       *APIServer
	mods      []Mod
	dagDigest digest.Digest

	// should not be read directly, call Schema and SchemaIntrospectionJSON instead
	lazilyLoadedSchema            *CompiledSchema
	lazilyLoadedIntrospectionJSON string
	loadSchemaErr                 error
	loadSchemaLock                sync.Mutex
}

func newModDeps(api *APIServer, mods []Mod) (*ModDeps, error) {
	seen := map[digest.Digest]struct{}{}
	finalMods := make([]Mod, 0, len(mods))
	for _, mod := range mods {
		dagDigest := mod.DagDigest()
		if _, ok := seen[dagDigest]; ok {
			continue
		}
		seen[dagDigest] = struct{}{}
		finalMods = append(finalMods, mod)
	}
	sort.Slice(finalMods, func(i, j int) bool {
		return finalMods[i].DagDigest().String() < finalMods[j].DagDigest().String()
	})
	dagDigests := make([]string, 0, len(finalMods))
	for _, mod := range finalMods {
		dagDigests = append(dagDigests, mod.DagDigest().String())
	}
	dagDigest := digest.FromString(strings.Join(dagDigests, " "))

	return &ModDeps{
		api:       api,
		mods:      mods,
		dagDigest: dagDigest,
	}, nil
}

// The digest of all the modules in the DAG
func (d *ModDeps) DagDigest() digest.Digest {
	return d.dagDigest
}

// The combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) Schema(ctx context.Context) (*CompiledSchema, error) {
	schema, _, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

// The introspection json for combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) SchemaIntrospectionJSON(ctx context.Context) (string, error) {
	_, introspectionJSON, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return "", err
	}
	return introspectionJSON, nil
}

func (d *ModDeps) lazilyLoadSchema(ctx context.Context) (loadedSchema *CompiledSchema, loadedIntrospectionJSON string, rerr error) {
	d.loadSchemaLock.Lock()
	defer d.loadSchemaLock.Unlock()
	if d.lazilyLoadedSchema != nil {
		return d.lazilyLoadedSchema, d.lazilyLoadedIntrospectionJSON, nil
	}
	if d.loadSchemaErr != nil {
		return nil, "", d.loadSchemaErr
	}
	defer func() {
		d.lazilyLoadedSchema = loadedSchema
		d.lazilyLoadedIntrospectionJSON = loadedIntrospectionJSON
		d.loadSchemaErr = rerr
	}()

	var schemas []SchemaResolvers
	for _, mod := range d.mods {
		modSchemas, err := mod.Schema(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}
		schemas = append(schemas, modSchemas...)
	}
	schema, err := mergeSchemaResolvers(schemas...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to merge schemas: %w", err)
	}
	introspectionJSON, err := schemaIntrospectionJSON(ctx, *schema.Compiled)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get schema introspection JSON: %w", err)
	}

	return schema, introspectionJSON, nil
}

// Search the deps for the given type def, returning the ModType if found. This does not recurse
// to transitive dependencies; it only returns types directly exposed by the schema of the top-level
// deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *core.TypeDef) (ModType, bool, error) {
	for _, mod := range d.mods {
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
