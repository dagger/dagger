package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/graphql"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"

	coreModuleName = "daggercore"
)

// TODO: consider renaming to ModDependencies, methods are performed on roots, not full DAG
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

// TODO: consider validating that there are no duplicate modules in the DAG
// TODO: consider validating that there are no cycles in the DAG (either here or elsewhere)
func newModDag(ctx context.Context, api *APIServer, roots []Mod) (*ModDag, error) {
	var dagDigests []string
	for _, root := range roots {
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

func (d *ModDag) ModTypeFor(ctx context.Context, typeDef *core.TypeDef) (ModType, bool, error) {
	for _, root := range d.roots {
		modType, ok, err := root.ModTypeFor(ctx, typeDef)
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
	ModTypeFor(context.Context, *core.TypeDef) (ModType, bool, error)
}

type ModType interface {
	ConvertFromSDKResult(ctx context.Context, value any) (any, error)
	ConvertToSDKInput(ctx context.Context, value any) (any, error)
	SourceMod() Mod
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

func (m *CoreMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef) (ModType, bool, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return &PrimitiveType{}, true, nil

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.ElementTypeDef)
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

type CoreModObject struct {
	coreMod  *CoreMod
	resolver IDableObjectResolver
}

var _ ModType = (*CoreModObject)(nil)

func (obj *CoreModObject) ConvertFromSDKResult(_ context.Context, value any) (any, error) {
	id, ok := value.(string)
	if !ok {
		return value, nil
	}
	return obj.resolver.FromID(id)
}

func (obj *CoreModObject) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	if _, ok := value.(string); ok {
		return value, nil
	}
	return obj.resolver.ToID(value)
}

func (obj *CoreModObject) SourceMod() Mod {
	return obj.coreMod
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

	// TODO: re-doc this
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

func (m *UserMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef) (ModType, bool, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return &PrimitiveType{}, true, nil

	case core.TypeDefKindList:
		underlyingType, ok, err := m.ModTypeFor(ctx, typeDef.AsList.ElementTypeDef)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get underlying type: %w", err)
		}
		if !ok {
			return nil, false, nil
		}
		return &ListType{underlying: underlyingType}, true, nil

	case core.TypeDefKindObject:
		// check to see if this is from a direct dependency
		// TODO: this is wrong; it ends up recursing to non-direct deps
		for _, depMod := range m.deps.roots {
			depType, ok, err := depMod.ModTypeFor(ctx, typeDef)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get type from dependency %q: %w", depMod.Name(), err)
			}
			if !ok {
				continue
			}
			return depType, true, nil
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

type ListType struct {
	underlying ModType
}

func (t *ListType) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", value)
	}
	resultList := make([]any, len(list))
	for i, item := range list {
		var err error
		resultList[i], err = t.underlying.ConvertFromSDKResult(ctx, item)
		if err != nil {
			return nil, err
		}
	}
	return resultList, nil
}

func (t *ListType) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", value)
	}
	resultList := make([]any, len(list))
	for i, item := range list {
		var err error
		resultList[i], err = t.underlying.ConvertToSDKInput(ctx, item)
		if err != nil {
			return nil, err
		}
	}
	return resultList, nil
}

func (t *ListType) SourceMod() Mod {
	return t.underlying.SourceMod()
}

type PrimitiveType struct{}

func (t *PrimitiveType) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	return value, nil
}

func (t *PrimitiveType) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	return value, nil
}

func (t *PrimitiveType) SourceMod() Mod {
	return nil
}

type UserModObject struct {
	api     *APIServer
	mod     *UserMod
	typeDef *core.TypeDef

	// should not be read directly, call Fields() and Functions() instead
	lazyLoadedFields    []*UserModField
	lazyLoadedFunctions []*UserModFunction
	loadErr             error
	loadLock            sync.Mutex
}

var _ ModType = (*UserModObject)(nil)

func newModObject(ctx context.Context, mod *UserMod, typeDef *core.TypeDef) (*UserModObject, error) {
	if typeDef.Kind != core.TypeDefKindObject {
		return nil, fmt.Errorf("expected object type def, got %s", typeDef.Kind)
	}
	obj := &UserModObject{
		api:     mod.api,
		mod:     mod,
		typeDef: typeDef,
	}
	return obj, nil
}

func (obj *UserModObject) TypeDef() *core.TypeDef {
	return obj.typeDef
}

func (obj *UserModObject) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		decodedMap, err := resourceid.DecodeModuleID(value, obj.typeDef.AsObject.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to decode module id: %w", err)
		}
		return obj.ConvertFromSDKResult(ctx, decodedMap)
	case map[string]any:
		fields, err := obj.Fields(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get fields: %w", err)
		}
		fieldNameToModType := make(map[string]ModType) // TODO: cache
		for _, field := range fields {
			fieldNameToModType[field.metadata.Name] = field.modType
		}
		for k, v := range value {
			normalizedName := gqlFieldName(k)
			fieldType, ok := fieldNameToModType[normalizedName]
			if !ok {
				continue
			}
			delete(value, k)
			value[normalizedName], err = fieldType.ConvertFromSDKResult(ctx, v)
			if err != nil {
				return nil, fmt.Errorf("failed to convert field %q: %w", k, err)
			}
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unexpected result value type %T for object %q", value, obj.typeDef.AsObject.Name)
	}
}

func (obj *UserModObject) ConvertToSDKInput(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	// TODO: in theory it's more correct to convert to an ID, but SDKs don't currently handle this correctly.
	// They only do ID conversions for core types, but still expect custom objects to be passed as raw
	// json serialized objects. This can be updated once SDKs are fixed
	switch value := value.(type) {
	case string:
		// TODO: this is what should be happening
		// return value, nil
		return resourceid.DecodeModuleID(value, obj.typeDef.AsObject.Name)
	case map[string]any:
		// TODO: this is what should be happening
		// return resourceid.EncodeModule(obj.typeDef.AsObject.Name, value)

		fields, err := obj.Fields(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get fields: %w", err)
		}
		fieldNameToModType := make(map[string]ModType) // TODO: cache
		for _, field := range fields {
			fieldNameToModType[field.metadata.Name] = field.modType
		}
		for k, v := range value {
			normalizedName := gqlFieldName(k)
			fieldType, ok := fieldNameToModType[normalizedName]
			if !ok {
				continue
			}
			value[k], err = fieldType.ConvertToSDKInput(ctx, v)
			if err != nil {
				return nil, fmt.Errorf("failed to convert field %q: %w", k, err)
			}
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unexpected input value type %T for object %q", value, obj.typeDef.AsObject.Name)
	}
}

func (obj *UserModObject) SourceMod() Mod {
	return obj.mod
}

func (obj *UserModObject) Fields(ctx context.Context) ([]*UserModField, error) {
	fields, _, err := obj.loadFieldsAndFunctions(ctx)
	if err != nil {
		return nil, err
	}
	return fields, nil
}

func (obj *UserModObject) Functions(ctx context.Context) ([]*UserModFunction, error) {
	_, functions, err := obj.loadFieldsAndFunctions(ctx)
	if err != nil {
		return nil, err
	}
	return functions, nil
}

func (obj *UserModObject) loadFieldsAndFunctions(ctx context.Context) (
	loadedFields []*UserModField, loadedFunctions []*UserModFunction, rerr error,
) {
	obj.loadLock.Lock()
	defer obj.loadLock.Unlock()
	if len(obj.lazyLoadedFields) > 0 || len(obj.lazyLoadedFunctions) > 0 {
		return obj.lazyLoadedFields, obj.lazyLoadedFunctions, nil
	}
	if obj.loadErr != nil {
		return nil, nil, obj.loadErr
	}
	defer func() {
		obj.lazyLoadedFields = loadedFields
		obj.lazyLoadedFunctions = loadedFunctions
		obj.loadErr = rerr
	}()

	runtime, err := obj.mod.Runtime(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get module runtime: %w", err)
	}

	for _, fieldTypeDef := range obj.typeDef.AsObject.Fields {
		modField, err := newModField(ctx, obj, fieldTypeDef)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create field: %w", err)
		}
		loadedFields = append(loadedFields, modField)
	}
	for _, fn := range obj.typeDef.AsObject.Functions {
		modFunction, err := newModFunction(ctx, obj.mod, obj, runtime, fn)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create function: %w", err)
		}
		loadedFunctions = append(loadedFunctions, modFunction)
	}
	return loadedFields, loadedFunctions, nil
}

func (obj *UserModObject) FunctionByName(ctx context.Context, name string) (*UserModFunction, error) {
	_, functions, err := obj.loadFieldsAndFunctions(ctx)
	if err != nil {
		return nil, err
	}

	name = gqlFieldName(name)
	for _, fn := range functions {
		if gqlFieldName(fn.metadata.Name) == name {
			return fn, nil
		}
	}

	return nil, fmt.Errorf("failed to find function %q", name)
}

func (obj *UserModObject) Schema(ctx context.Context) (*ast.SchemaDocument, Resolvers, error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("object", obj.typeDef.AsObject.Name))
	bklog.G(ctx).Debug("getting object schema")

	fields, functions, err := obj.loadFieldsAndFunctions(ctx)
	if err != nil {
		return nil, nil, err
	}

	typeSchemaDoc := &ast.SchemaDocument{}
	queryResolver := ObjectResolver{}
	typeSchemaResolvers := Resolvers{
		"Query": queryResolver,
	}

	objTypeDef := obj.typeDef.AsObject
	objName := gqlObjectName(objTypeDef.Name)

	// get the schema + resolvers for the object as a whole
	objASTType, err := typeDefToASTType(obj.typeDef, false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert object to schema: %w", err)
	}

	// check whether this is a pre-existing object from a dependency module
	modType, ok, err := obj.mod.deps.ModTypeFor(ctx, obj.typeDef)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get mod type for type def: %w", err)
	}
	if ok {
		if sourceMod := modType.SourceMod(); sourceMod != nil && sourceMod.DagDigest() != obj.mod.DagDigest() {
			// modules can reference types from core/other modules as types, but they
			// can't attach any new fields or functions to them
			if len(objTypeDef.Fields) > 0 || len(objTypeDef.Functions) > 0 {
				return nil, nil, fmt.Errorf("cannot attach new fields or functions to object %q from outside module", objName)
			}
			return nil, nil, nil
		}
	}

	astDef := &ast.Definition{
		Name:        objName,
		Description: formatGqlDescription(objTypeDef.Description),
		Kind:        ast.Object,
	}
	astIDDef := &ast.Definition{
		Name:        objName + "ID",
		Description: formatGqlDescription("A reference to %s", objName),
		Kind:        ast.Scalar,
	}
	astLoadDef := &ast.FieldDefinition{
		Name:        fmt.Sprintf("load%sFromID", objName),
		Description: formatGqlDescription("Loads a %s from an ID", objName),
		Arguments: ast.ArgumentDefinitionList{
			&ast.ArgumentDefinition{
				Name: "id",
				Type: ast.NonNullNamedType(objName+"ID", nil),
			},
		},
		Type: ast.NonNullNamedType(objName, nil),
	}

	newObjResolver := ObjectResolver{}

	astDef.Fields = append(astDef.Fields, &ast.FieldDefinition{
		Name:        "id",
		Description: formatGqlDescription("A unique identifier for a %s", objName),
		Type:        ast.NonNullNamedType(objName+"ID", nil),
	})
	newObjResolver["id"] = func(p graphql.ResolveParams) (any, error) {
		return resourceid.EncodeModule(objName, p.Source)
	}

	for _, field := range fields {
		fieldDef, resolver, err := field.Schema(ctx)
		if err != nil {
			return nil, nil, err
		}
		astDef.Fields = append(astDef.Fields, fieldDef)
		newObjResolver[fieldDef.Name] = resolver
	}

	for _, fn := range functions {
		fieldDef, resolver, err := fn.Schema(ctx)
		if err != nil {
			return nil, nil, err
		}
		astDef.Fields = append(astDef.Fields, fieldDef)
		newObjResolver[fieldDef.Name] = resolver
	}

	if len(newObjResolver) > 0 {
		typeSchemaResolvers[objName] = newObjResolver
		typeSchemaResolvers[objName+"ID"] = stringResolver[string]()

		queryResolver[fmt.Sprintf("load%sFromID", objName)] = func(p graphql.ResolveParams) (any, error) {
			// TODO: name of method is awkward in this case... better name?
			return obj.ConvertFromSDKResult(ctx, p.Args["id"])
		}
	}

	// handle object constructor
	var constructorFieldDef *ast.FieldDefinition
	var constructorResolver graphql.FieldResolveFn
	isMainModuleObject := objName == gqlObjectName(obj.mod.metadata.Name)
	if isMainModuleObject {
		constructorFieldDef = &ast.FieldDefinition{
			Name:        gqlFieldName(objName),
			Description: formatGqlDescription(objTypeDef.Description),
			Type:        objASTType,
		}

		if objTypeDef.Constructor != nil {
			// use explicit user-defined constructor if provided
			fnTypeDef := objTypeDef.Constructor
			if fnTypeDef.ReturnType.Kind != core.TypeDefKindObject {
				return nil, nil, fmt.Errorf("constructor function for object %s must return that object", objTypeDef.OriginalName)
			}
			if fnTypeDef.ReturnType.AsObject.OriginalName != objTypeDef.OriginalName {
				return nil, nil, fmt.Errorf("constructor function for object %s must return that object", objTypeDef.OriginalName)
			}

			runtime, err := obj.mod.Runtime(ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get module runtime: %w", err)
			}
			fn, err := newModFunction(ctx, obj.mod, obj, runtime, fnTypeDef)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create function: %w", err)
			}

			fieldDef, resolver, err := fn.Schema(ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get schema for constructor function: %w", err)
			}
			constructorFieldDef.Arguments = fieldDef.Arguments
			constructorResolver = resolver
		} else {
			// otherwise default to a simple field with no args that returns an initially empty object
			constructorResolver = PassthroughResolver
		}

		typeSchemaDoc.Extensions = append(typeSchemaDoc.Extensions, &ast.Definition{
			Name:   "Query",
			Kind:   ast.Object,
			Fields: ast.FieldList{constructorFieldDef},
		})
		queryResolver[constructorFieldDef.Name] = constructorResolver
	}

	if len(astDef.Fields) > 0 || constructorFieldDef != nil {
		typeSchemaDoc.Definitions = append(typeSchemaDoc.Definitions, astDef, astIDDef)
		typeSchemaDoc.Extensions = append(typeSchemaDoc.Extensions, &ast.Definition{
			Name:   "Query",
			Kind:   ast.Object,
			Fields: ast.FieldList{astLoadDef},
		})
	}

	return typeSchemaDoc, typeSchemaResolvers, nil
}

type UserModField struct {
	obj      *UserModObject
	metadata *core.FieldTypeDef
	modType  ModType
}

func newModField(ctx context.Context, obj *UserModObject, metadata *core.FieldTypeDef) (*UserModField, error) {
	modType, ok, err := obj.mod.ModTypeFor(ctx, metadata.TypeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to get mod type for field %q: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to get mod type for field %q", metadata.Name)
	}
	return &UserModField{
		obj:      obj,
		metadata: metadata,
		modType:  modType,
	}, nil
}

func (f *UserModField) Schema(ctx context.Context) (*ast.FieldDefinition, graphql.FieldResolveFn, error) {
	typeDef := f.metadata.TypeDef
	fieldASTType, err := typeDefToASTType(typeDef, false)
	if err != nil {
		return nil, nil, err
	}

	// Check if this is a type from another (non-core) module, which is currently not allowed
	sourceMod := f.modType.SourceMod()
	if sourceMod != nil && sourceMod.Name() != coreModuleName && sourceMod.DagDigest() != f.obj.mod.DagDigest() {
		return nil, nil, fmt.Errorf("object %q field %q cannot reference external type from dependency module %q",
			f.obj.typeDef.AsObject.OriginalName,
			f.metadata.OriginalName,
			sourceMod.Name(),
		)
	}

	fieldDef := &ast.FieldDefinition{
		Name:        f.metadata.Name,
		Description: formatGqlDescription(f.metadata.Description),
		Type:        fieldASTType,
	}
	return fieldDef, func(p graphql.ResolveParams) (any, error) {
		res, err := graphql.DefaultResolveFn(p)
		if err != nil {
			return nil, err
		}
		return f.modType.ConvertFromSDKResult(ctx, res)
	}, nil
}

type UserModFunction struct {
	api     *APIServer
	mod     *UserMod
	obj     *UserModObject // may be nil for special functions like the module definition function call
	runtime *core.Container

	metadata   *core.Function
	returnType ModType
	args       map[string]*UserModFunctionArg
}

type UserModFunctionArg struct {
	metadata *core.FunctionArg
	modType  ModType
}

func newModFunction(
	ctx context.Context,
	mod *UserMod,
	obj *UserModObject,
	runtime *core.Container,
	metadata *core.Function,
) (*UserModFunction, error) {
	returnType, ok, err := mod.ModTypeFor(ctx, metadata.ReturnType)
	if err != nil {
		return nil, fmt.Errorf("failed to get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find mod type for function %q return type", metadata.Name)
	}

	argTypes := make(map[string]*UserModFunctionArg, len(metadata.Args))
	for _, argMetadata := range metadata.Args {
		argModType, ok, err := mod.ModTypeFor(ctx, argMetadata.TypeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for function %q arg %q type: %w", metadata.Name, argMetadata.Name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for function %q arg %q type", metadata.Name, argMetadata.Name)
		}
		argTypes[argMetadata.Name] = &UserModFunctionArg{
			metadata: argMetadata,
			modType:  argModType,
		}
	}

	return &UserModFunction{
		api:        mod.api,
		mod:        mod,
		obj:        obj,
		runtime:    runtime,
		metadata:   metadata,
		returnType: returnType,
		args:       argTypes,
	}, nil
}

func (fn *UserModFunction) Digest() digest.Digest {
	inputs := []string{
		fn.mod.DagDigest().String(),
		fn.metadata.Name,
	}
	if fn.obj != nil {
		inputs = append(inputs, fn.obj.typeDef.AsObject.Name)
	}
	return digest.FromString(strings.Join(inputs, " "))
}

func (fn *UserModFunction) Schema(ctx context.Context) (*ast.FieldDefinition, graphql.FieldResolveFn, error) {
	fnName := gqlFieldName(fn.metadata.Name)
	var objFnName string
	if fn.obj != nil {
		objFnName = fmt.Sprintf("%s.%s", fn.obj.typeDef.AsObject.Name, fnName)
	} else {
		objFnName = fnName
	}

	returnASTType, err := typeDefToASTType(fn.metadata.ReturnType, false)
	if err != nil {
		return nil, nil, err
	}

	// Check if this is a type from another (non-core) module, which is currently not allowed
	sourceMod := fn.returnType.SourceMod()
	if sourceMod != nil && sourceMod.Name() != coreModuleName && sourceMod.DagDigest() != fn.mod.DagDigest() {
		var objName string
		if fn.obj != nil {
			objName = fn.obj.typeDef.AsObject.OriginalName
		}
		return nil, nil, fmt.Errorf("object %q function %q cannot return external type from dependency module %q",
			objName,
			fn.metadata.OriginalName,
			sourceMod.Name(),
		)
	}

	fieldDef := &ast.FieldDefinition{
		Name:        fnName,
		Description: formatGqlDescription(fn.metadata.Description),
		Type:        returnASTType,
	}

	for _, argMetadata := range fn.metadata.Args {
		arg, ok := fn.args[argMetadata.Name]
		if !ok {
			return nil, nil, fmt.Errorf("failed to find arg %q", argMetadata.Name)
		}

		argASTType, err := typeDefToASTType(argMetadata.TypeDef, true)
		if err != nil {
			return nil, nil, err
		}

		// Check if this is a type from another (non-core) module, which is currently not allowed
		sourceMod := arg.modType.SourceMod()
		if sourceMod != nil && sourceMod.Name() != coreModuleName && sourceMod.DagDigest() != fn.mod.DagDigest() {
			var objName string
			if fn.obj != nil {
				objName = fn.obj.typeDef.AsObject.OriginalName
			}
			return nil, nil, fmt.Errorf("object %q function %q arg %q cannot reference external type from dependency module %q",
				objName,
				fn.metadata.OriginalName,
				argMetadata.OriginalName,
				sourceMod.Name(),
			)
		}

		defaultValue, err := astDefaultValue(argMetadata.TypeDef, argMetadata.DefaultValue)
		if err != nil {
			return nil, nil, err
		}
		argDef := &ast.ArgumentDefinition{
			Name:         gqlArgName(argMetadata.Name),
			Description:  formatGqlDescription(argMetadata.Description),
			Type:         argASTType,
			DefaultValue: defaultValue,
		}
		fieldDef.Arguments = append(fieldDef.Arguments, argDef)
	}

	resolver := ToResolver(func(ctx context.Context, parent any, args map[string]any) (_ any, rerr error) {
		defer func() {
			if r := recover(); r != nil {
				rerr = fmt.Errorf("panic in %s: %s %s", objFnName, r, string(debug.Stack()))
			}
		}()

		var callInput []*core.CallInput
		for k, v := range args {
			callInput = append(callInput, &core.CallInput{
				Name:  k,
				Value: v,
			})
		}
		return fn.Call(ctx, false, nil, parent, callInput)
	})

	return fieldDef, resolver, nil
}

func (fn *UserModFunction) Call(ctx context.Context, cache bool, pipeline pipeline.Path, parentVal any, inputs []*core.CallInput) (any, error) {
	lg := bklog.G(ctx).WithField("module", fn.mod.Name()).WithField("function", fn.metadata.Name)
	if fn.obj != nil {
		lg = lg.WithField("object", fn.obj.typeDef.AsObject.Name)
	}
	ctx = bklog.WithLogger(ctx, lg)

	callerDigestInputs := []string{fn.Digest().String()}

	if fn.obj != nil {
		// TODO: add parent val to cache keys

		var err error
		parentVal, err = fn.obj.ConvertToSDKInput(ctx, parentVal)
		if err != nil {
			return nil, fmt.Errorf("failed to convert parent value: %w", err)
		}
	}

	for _, input := range inputs {
		normalizedName := gqlArgName(input.Name)
		arg, ok := fn.args[normalizedName]
		if !ok {
			return nil, fmt.Errorf("failed to find arg %q", input.Name)
		}
		input.Name = arg.metadata.OriginalName

		var err error
		input.Value, err = arg.modType.ConvertToSDKInput(ctx, input.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert arg %q: %w", input.Name, err)
		}

		dgst, err := input.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get arg digest: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, dgst.String())
	}

	if !cache {
		// use the ServerID so that we bust cache once-per-session
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, clientMetadata.ServerID)
	}

	callerDigest := digest.FromString(strings.Join(callerDigestInputs, " "))

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("caller_digest", callerDigest.String()))
	bklog.G(ctx).Debug("function call")
	defer func() {
		bklog.G(ctx).Debug("function call done")
	}()

	ctr := fn.runtime

	metaDir := core.NewScratchDirectory(pipeline, fn.api.platform)
	ctr, err := ctr.WithMountedDirectory(ctx, fn.api.bk, modMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Setup the Exec for the Function call and evaluate it
	ctr, err = ctr.WithExec(ctx, fn.api.bk, fn.api.progSockPath, fn.api.platform, core.ContainerExecOpts{
		// TODO: rename ModuleContextDigest
		ModuleContextDigest:           callerDigest,
		ExperimentalPrivilegedNesting: true,
		NestedInSameSession:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	callMeta := &core.FunctionCall{
		Name:      fn.metadata.OriginalName,
		Parent:    parentVal,
		InputArgs: inputs,
	}
	if fn.obj != nil {
		callMeta.ParentName = fn.obj.typeDef.AsObject.OriginalName
	}

	err = fn.api.RegisterFunctionCall(ctx, callerDigest, fn.mod, callMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to register function call: %w", err)
	}

	ctrOutputDir, err := ctr.Directory(ctx, fn.api.bk, fn.api.services, modMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get function output directory: %w", err)
	}

	result, err := ctrOutputDir.Evaluate(ctx, fn.api.bk, fn.api.services)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate function: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("function returned nil result")
	}

	// TODO: if any error happens below, we should really prune the cache of the result, otherwise
	// we can end up in a state where we have a cached result with a dependency blob that we don't
	// guarantee the continued existence of...

	// Read the output of the function
	outputBytes, err := result.Ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: modMetaOutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read function output file: %w", err)
	}

	var returnValue any
	if err := json.Unmarshal(outputBytes, &returnValue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %s", err)
	}

	returnValue, err = fn.returnType.ConvertFromSDKResult(ctx, returnValue)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return value: %w", err)
	}

	if err := fn.linkDependencyBlobs(ctx, result, returnValue, fn.metadata.ReturnType); err != nil {
		return nil, fmt.Errorf("failed to link dependency blobs: %w", err)
	}

	return returnValue, nil
}

// If the result of a Function call contains IDs of resources, we need to ensure that the cache entry for the
// Function call is linked for the cache entries of those resources if those entries aren't reproducible.
// Right now, the only unreproducible output are local dir imports, which are represented as blob:// sources.
// linkDependencyBlobs finds all such blob:// sources and adds a cache lease on that blob in the content store
// to the cacheResult of the function call.
//
// If we didn't do this, then it would be possible for Buildkit to prune the content pointed to by the blob://
// source without pruning the function call cache entry. That would result callers being able to evaluate the
// result of a function call but hitting an error about missing content.

// TODO: check if this can be simplified/integrated w/ related code that walks types
func (fn *UserModFunction) linkDependencyBlobs(ctx context.Context, cacheResult *buildkit.Result, value any, typeDef *core.TypeDef) error {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger,
		core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return nil
	case core.TypeDefKindList:
		listValue, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected list value, got %T", value)
		}
		for _, elem := range listValue {
			if err := fn.linkDependencyBlobs(ctx, cacheResult, elem, typeDef.AsList.ElementTypeDef); err != nil {
				return fmt.Errorf("failed to link dependency blobs: %w", err)
			}
		}
		return nil
	case core.TypeDefKindObject:
		if mapValue, ok := value.(map[string]any); ok {
			// This object is not a core type but we still need to check its
			// Fields for any objects that may contain core objects
			for fieldName, fieldValue := range mapValue {
				field, ok := typeDef.AsObject.FieldByName(fieldName)
				if !ok {
					continue
				}
				if err := fn.linkDependencyBlobs(ctx, cacheResult, fieldValue, field.TypeDef); err != nil {
					return fmt.Errorf("failed to link dependency blobs: %w", err)
				}
			}
			return nil
		}

		if pbDefinitioner, ok := value.(core.HasPBDefinitions); ok {
			pbDefs, err := pbDefinitioner.PBDefinitions()
			if err != nil {
				return fmt.Errorf("failed to get pb definitions: %w", err)
			}
			dependencyBlobs := map[digest.Digest]*ocispecs.Descriptor{}
			for _, pbDef := range pbDefs {
				dag, err := buildkit.DefToDAG(pbDef)
				if err != nil {
					return fmt.Errorf("failed to convert pb definition to dag: %w", err)
				}
				blobs, err := dag.BlobDependencies()
				if err != nil {
					return fmt.Errorf("failed to get blob dependencies: %w", err)
				}
				for k, v := range blobs {
					dependencyBlobs[k] = v
				}
			}

			if err := cacheResult.Ref.AddDependencyBlobs(ctx, dependencyBlobs); err != nil {
				return fmt.Errorf("failed to add dependency blob: %w", err)
			}
			return nil
		}

		// no dependency blobs to handle
		return nil
	default:
		return fmt.Errorf("unhandled type def kind %q", typeDef.Kind)
	}
}
