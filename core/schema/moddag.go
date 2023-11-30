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

type ModDag struct {
	api       *APIServer
	roots     []Mod
	dagDigest digest.Digest

	// should not be read directly, call Schema and SchemaIntrospectionJSON instead
	lazilyLoadedSchema            *CompiledSchema
	lazilyLoadedIntrospectionJSON string
	loadSchemaErr                 error
	loadSchemaLock                sync.Mutex
}

// TODO: consider validating that there are no cycles in the DAG (either here or elsewhere)
func newModDag(ctx context.Context, api *APIServer, roots []Mod) (*ModDag, error) {
	seen := map[digest.Digest]struct{}{}
	var finalRoots []Mod
	for _, root := range roots {
		dagDigest := root.DagDigest()
		if _, ok := seen[dagDigest]; ok {
			continue
		}
		seen[dagDigest] = struct{}{}
		finalRoots = append(finalRoots, root)
	}
	sort.Slice(finalRoots, func(i, j int) bool {
		return finalRoots[i].DagDigest().String() < finalRoots[j].DagDigest().String()
	})
	var dagDigests []string
	for _, root := range finalRoots {
		dagDigests = append(dagDigests, root.DagDigest().String())
	}
	dagDigest := digest.FromString(strings.Join(dagDigests, " "))

	return &ModDag{
		api:       api,
		roots:     roots,
		dagDigest: dagDigest,
	}, nil
}

func (d *ModDag) DagDigest() digest.Digest {
	return d.dagDigest
}

func (d *ModDag) Schema(ctx context.Context) (*CompiledSchema, error) {
	schema, _, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

func (d *ModDag) SchemaIntrospectionJSON(ctx context.Context) (string, error) {
	_, introspectionJSON, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return "", err
	}
	return introspectionJSON, nil
}

func (d *ModDag) lazilyLoadSchema(ctx context.Context) (loadedSchema *CompiledSchema, loadedIntrospectionJSON string, rerr error) {
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
	for _, root := range d.roots {
		modSchemas, err := root.Schema(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get schema for module %q: %w", root.Name(), err)
		}
		schemas = append(schemas, modSchemas...)
	}
	schema, err := mergeExecutableSchemas(schemas...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to merge schemas: %w", err)
	}
	introspectionJSON, err := schemaIntrospectionJSON(ctx, *schema.Compiled)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get schema introspection JSON: %w", err)
	}

	return schema, introspectionJSON, nil
}

// Search the *roots* of the DAG for the given type def, returning the ModType if found. This does
// not recurse to transitive dependencies; it only returns types directly exposed by the schema
// of this DAG.
func (d *ModDag) ModTypeFor(ctx context.Context, typeDef *core.TypeDef) (ModType, bool, error) {
	for _, root := range d.roots {
		modType, ok, err := root.ModTypeFor(ctx, typeDef, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get type from root %q: %w", root.Name(), err)
		}
		if !ok {
			continue
		}
		return modType, true, nil
	}
	return nil, false, nil
}

type Mod interface {
	Name() string
	DagDigest() digest.Digest
	Dependencies() []Mod
	Schema(context.Context) ([]SchemaResolvers, error)
	DependencySchemaIntrospectionJSON(context.Context) (string, error)
	ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error)
}

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

func (m *CoreMod) DependencySchemaIntrospectionJSON(_ context.Context) (string, error) {
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

type UserMod struct {
	api      *APIServer
	metadata *core.Module
	deps     *ModDag
	sdk      SDK

	dagDigest digest.Digest

	// should not be read directly, call m.Objects() instead
	lazilyLoadedObjects []*UserModObject
	loadObjectsErr      error
	loadObjectsLock     sync.Mutex
}

var _ Mod = (*UserMod)(nil)

func newUserMod(api *APIServer, modMeta *core.Module, deps *ModDag, sdk SDK) (*UserMod, error) {
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
	return m.deps.roots
}

func (m *UserMod) Codegen(ctx context.Context) (*core.GeneratedCode, error) {
	return m.sdk.Codegen(ctx, m)
}

func (m *UserMod) Runtime(ctx context.Context) (*core.Container, error) {
	return m.sdk.Runtime(ctx, m)
}

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
	result, err := getModDefFn.Call(ctx, true, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call module %q to get functions: %w", m.Name(), err)
	}

	modMeta, ok := result.(*core.Module)
	if !ok {
		return nil, fmt.Errorf("expected ModuleMetadata result, got %T", result)
	}

	var objs []*UserModObject
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

func (m *UserMod) DependencySchemaIntrospectionJSON(ctx context.Context) (string, error) {
	return m.deps.SchemaIntrospectionJSON(ctx)
}

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
