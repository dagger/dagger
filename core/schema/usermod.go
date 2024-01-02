package schema

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/graphql"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

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
	lazilyLoadedIfaces  []*InterfaceType
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
func (m *UserMod) Objects(ctx context.Context) ([]*UserModObject, error) {
	objs, _, err := m.objectsAndInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	return objs, nil
}

// The interfaces defined by this module, with namespacing applied
func (m *UserMod) Interfaces(ctx context.Context) ([]*InterfaceType, error) {
	_, ifaces, err := m.objectsAndInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	return ifaces, nil
}

func (m *UserMod) objectsAndInterfaces(ctx context.Context) (loadedObjects []*UserModObject, loadedIfaces []*InterfaceType, rerr error) {
	m.loadObjectsLock.Lock()
	defer m.loadObjectsLock.Unlock()
	if len(m.lazilyLoadedObjects) > 0 || len(m.lazilyLoadedIfaces) > 0 {
		return m.lazilyLoadedObjects, m.lazilyLoadedIfaces, nil
	}
	if m.loadObjectsErr != nil {
		return nil, nil, m.loadObjectsErr
	}

	defer func() {
		m.lazilyLoadedObjects = loadedObjects
		m.lazilyLoadedIfaces = loadedIfaces
		m.loadObjectsErr = rerr
	}()

	runtime, err := m.Runtime(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get module runtime: %w", err)
	}

	// construct a special function with no object or function name, which tells the SDK to return the module's definition
	// (in terms of objects, fields and functions)
	getModDefFn, err := newModFunction(ctx, m, nil, runtime, core.NewFunction("", &core.TypeDef{
		Kind:     core.TypeDefKindObject,
		AsObject: core.NewObjectTypeDef("Module", ""),
	}))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create module definition function for module %q: %w", m.Name(), err)
	}
	result, err := getModDefFn.Call(ctx, &CallOpts{Cache: true, SkipSelfSchema: true})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to call module %q to get functions: %w", m.Name(), err)
	}

	modMeta, ok := result.(*core.Module)
	if !ok {
		return nil, nil, fmt.Errorf("expected Module result, got %T", result)
	}

	objs := make([]*UserModObject, 0, len(modMeta.Objects))
	for _, objTypeDef := range modMeta.Objects {
		if err := m.validateTypeDef(ctx, objTypeDef); err != nil {
			return nil, nil, fmt.Errorf("failed to validate type def: %w", err)
		}

		if err := m.namespaceTypeDef(ctx, objTypeDef); err != nil {
			return nil, nil, fmt.Errorf("failed to namespace type def: %w", err)
		}

		obj, err := newModObject(ctx, m, objTypeDef)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create object: %w", err)
		}
		objs = append(objs, obj)
	}

	ifaces := make([]*InterfaceType, 0, len(modMeta.Interfaces))
	for _, ifaceTypeDef := range modMeta.Interfaces {
		if err := m.validateTypeDef(ctx, ifaceTypeDef); err != nil {
			return nil, nil, fmt.Errorf("failed to validate type def: %w", err)
		}

		if err := m.namespaceTypeDef(ctx, ifaceTypeDef); err != nil {
			return nil, nil, fmt.Errorf("failed to namespace type def: %w", err)
		}

		iface, err := newModIface(ctx, m, ifaceTypeDef)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create interface: %w", err)
		}
		ifaces = append(ifaces, iface)
	}

	return objs, ifaces, nil
}

func (m *UserMod) TypeDefs(ctx context.Context) ([]*core.TypeDef, error) {
	objs, err := m.Objects(ctx)
	if err != nil {
		return nil, err
	}
	typeDefs := make([]*core.TypeDef, 0, len(objs))
	for _, obj := range objs {
		typeDef := obj.typeDef.Clone()
		if typeDef.AsObject != nil {
			typeDef.AsObject.SourceModuleName = m.Name()
		}
		typeDefs = append(typeDefs, typeDef)
	}
	return typeDefs, nil
}

func (m *UserMod) ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString, core.TypeDefKindInteger, core.TypeDefKindBoolean, core.TypeDefKindVoid:
		return &PrimitiveType{kind: typeDef.Kind}, true, nil

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
				return nil, false, fmt.Errorf("failed to get object type from dependency: %w", err)
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

	case core.TypeDefKindInterface:
		if checkDirectDeps {
			// check to see if this is from a *direct* dependency
			depType, ok, err := m.deps.ModTypeFor(ctx, typeDef)
			if err != nil {
				return nil, false, fmt.Errorf("failed to get interface type from dependency: %w", err)
			}
			if ok {
				return depType, true, nil
			}
		}

		// otherwise it must be from this module
		ifaces, err := m.Interfaces(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, iface := range ifaces {
			if iface.typeDef.Name == typeDef.AsInterface.Name {
				return iface, true, nil
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

	objs, ifaces, err := m.objectsAndInterfaces(ctx)
	if err != nil {
		return nil, err
	}

	schemaResolvers := make([]SchemaResolvers, 0, len(objs)+len(ifaces))

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
		schemaResolvers = append(schemaResolvers, schema)
	}

	for _, iface := range ifaces {
		ifaceSchemaDoc, ifaceResolvers, err := iface.Schema(ctx)
		if err != nil {
			return nil, err
		}
		buf := &bytes.Buffer{}
		formatter.NewFormatter(buf).FormatSchemaDocument(ifaceSchemaDoc)
		typeSchemaStr := buf.String()

		schema := StaticSchema(StaticSchemaParams{
			Name:      fmt.Sprintf("%s.%s", m.metadata.Name, iface.typeDef.Name),
			Schema:    typeSchemaStr,
			Resolvers: ifaceResolvers,
		})
		schemaResolvers = append(schemaResolvers, schema)
	}

	return schemaResolvers, nil
}

func (m *UserMod) DependencySchemaIntrospectionJSON(ctx context.Context) (string, error) {
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
	case core.TypeDefKindInterface:
		iface := typeDef.AsInterface

		// check whether this is a pre-existing interface from core or another module
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

		for _, fn := range iface.Functions {
			if gqlFieldName(fn.Name) == "id" {
				return fmt.Errorf("cannot define function with reserved name %q on interface %q", fn.Name, iface.Name)
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
	case core.TypeDefKindInterface:
		iface := typeDef.AsInterface

		// only namespace interfaces defined in this module
		_, ok, err := m.deps.ModTypeFor(ctx, typeDef)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if !ok {
			iface.Name = namespaceObject(iface.Name, m.metadata.Name)
		}

		for _, fn := range iface.Functions {
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

// UserModObject is an object defined by a user module
type UserModObject struct {
	api *APIServer
	mod *UserMod
	// the type def metadata, with namespacing already applied
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

func (obj *UserModObject) ConvertFromSDKResult(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		modObjData, err := resourceid.DecodeModuleID(value, obj.typeDef.AsObject.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to decode module id: %w", err)
		}
		return obj.ConvertFromSDKResult(ctx, modObjData.Data)
	case map[string]any:
		for k, v := range value {
			normalizedName := gqlFieldName(k)
			field, ok, err := obj.FieldByName(ctx, normalizedName)
			if err != nil {
				return nil, fmt.Errorf("failed to get field %q: %w", k, err)
			}
			if !ok {
				continue
			}

			delete(value, k)
			value[normalizedName], err = field.modType.ConvertFromSDKResult(ctx, v)
			if err != nil {
				return nil, fmt.Errorf("failed to convert field result %q: %w", k, err)
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

	// NOTE: user mod objects are currently only passed as inputs to the module they originate from; modules
	// can't have inputs/outputs from other modules (other than core). These objects are also passed as their
	// direct json serialization rather than as an ID (so that SDKs can decode them without needing to make
	// calls to their own API).
	switch value := value.(type) {
	case string:
		modObjData, err := resourceid.DecodeModuleID(value, obj.typeDef.AsObject.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to decode module id: %w", err)
		}
		return modObjData.Data, nil
	case map[string]any:
		for k, v := range value {
			normalizedName := gqlFieldName(k)
			field, ok, err := obj.FieldByName(ctx, normalizedName)
			if err != nil {
				return nil, fmt.Errorf("failed to get field %q: %w", k, err)
			}
			if !ok {
				continue
			}

			delete(value, k)
			value[field.metadata.OriginalName], err = field.modType.ConvertToSDKInput(ctx, v)
			if err != nil {
				return nil, fmt.Errorf("failed to convert field input %q: %w", k, err)
			}
		}
		return value, nil
	case *interfaceRuntimeValue:
		return obj.ConvertToSDKInput(ctx, value.Value)
	default:
		return nil, fmt.Errorf("unexpected input value type %T for object %q", value, obj.typeDef.AsObject.Name)
	}
}

// ConvertToID converts the given value to an ID for this object. This is needed separately from ConvertToSDKInput
// due to the inconsistency mentioned in that method about how user objects are passed to their own module.
// Interface support requires actual conversion of the object to an ID and uses this method to do so.
func (obj *UserModObject) ConvertToID(ctx context.Context, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch value := value.(type) {
	case string:
		return value, nil
	case map[string]any:
		objMap, err := obj.ConvertToSDKInput(ctx, value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to sdk input: %w", err)
		}
		return resourceid.EncodeModule(&resourceid.ModuleObjectData{
			Data:      objMap,
			ModDigest: obj.mod.DagDigest(),
			TypeName:  obj.typeDef.AsObject.Name,
		})
	default:
		return nil, fmt.Errorf("unexpected input value type %T for object %q", value, obj.typeDef.AsObject.Name)
	}
}

func (obj *UserModObject) SourceMod() Mod {
	return obj.mod
}

func (obj *UserModObject) TypeDef() *core.TypeDef {
	return obj.typeDef.Clone()
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

func (obj *UserModObject) FieldByName(ctx context.Context, name string) (*UserModField, bool, error) {
	fields, _, err := obj.loadFieldsAndFunctions(ctx)
	if err != nil {
		return nil, false, err
	}

	name = gqlFieldName(name)
	for _, f := range fields {
		if gqlFieldName(f.metadata.Name) == name {
			return f, true, nil
		}
	}

	return nil, false, nil
}

func (obj *UserModObject) FunctionByName(ctx context.Context, name string) (*UserModFunction, bool, error) {
	_, functions, err := obj.loadFieldsAndFunctions(ctx)
	if err != nil {
		return nil, false, err
	}

	name = gqlFieldName(name)
	for _, fn := range functions {
		if gqlFieldName(fn.metadata.Name) == name {
			return fn, true, nil
		}
	}

	return nil, false, nil
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
		Description: formatGqlDescription("%s identifier", objName),
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
		Description: formatGqlDescription("A unique identifier for this %s", objName),
		Type:        ast.NonNullNamedType(objName+"ID", nil),
	})
	newObjResolver["id"] = func(p graphql.ResolveParams) (any, error) {
		return obj.ConvertToID(ctx, p.Source)
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
			res, err := obj.ConvertFromSDKResult(ctx, p.Args["id"])
			if err != nil {
				return nil, fmt.Errorf("failed to load object %s from id: %w", objName, err)
			}
			return res, nil
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

func (obj *UserModObject) InterfaceExtensionsSchema(ctx context.Context, ifaces []*InterfaceType) (SchemaResolvers, error) {
	if len(ifaces) == 0 {
		return nil, nil
	}

	typeSchemaDoc := &ast.SchemaDocument{}
	objResolver := ObjectResolver{}
	for _, iface := range ifaces {
		iface := iface
		if !obj.typeDef.AsObject.IsSubtypeOf(iface.typeDef) {
			continue
		}

		ifaceName := iface.typeDef.Name

		asIfaceFieldName := gqlFieldName(fmt.Sprintf("as%s", ifaceName))
		typeSchemaDoc.Extensions = append(typeSchemaDoc.Extensions, &ast.Definition{
			Name: obj.typeDef.AsObject.Name,
			Kind: ast.Object,
			Fields: ast.FieldList{{
				Name: asIfaceFieldName,
				Type: ast.NonNullNamedType(ifaceName, nil),
			}},
		})

		objResolver[asIfaceFieldName] = func(p graphql.ResolveParams) (any, error) {
			return &interfaceRuntimeValue{
				Value:          p.Source,
				UnderlyingType: obj,
				IfaceType:      iface,
			}, nil
		}
	}

	if len(objResolver) == 0 {
		return nil, nil
	}

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(typeSchemaDoc)
	typeSchemaStr := buf.String()
	return StaticSchema(StaticSchemaParams{
		Name:   fmt.Sprintf("%s.%s.interface_extensions", obj.mod.metadata.Name, obj.typeDef.AsObject.Name),
		Schema: typeSchemaStr,
		Resolvers: Resolvers{
			obj.typeDef.AsObject.Name: objResolver,
		},
	}), nil
}

func (obj *UserModObject) GetCallable(ctx context.Context, graphqlFieldName string) (Callable, error) {
	objField, ok, err := obj.FieldByName(ctx, graphqlFieldName)
	if err != nil {
		return nil, fmt.Errorf("failed to get field %s.%s: %w", obj.typeDef.AsObject.Name, graphqlFieldName, err)
	}
	if ok {
		return objField, nil
	}

	fn, ok, err := obj.FunctionByName(ctx, graphqlFieldName)
	if err != nil {
		return nil, fmt.Errorf("failed to get function %s.%s: %w", obj.typeDef.AsObject.Name, graphqlFieldName, err)
	}
	if !ok {
		return nil, fmt.Errorf("no field or function %s.%s", obj.typeDef.AsObject.Name, graphqlFieldName)
	}
	return fn, nil
}

type Callable interface {
	Call(context.Context, *CallOpts) (any, error)
	ReturnType() (ModType, error)
	ArgType(argName string) (ModType, error)
}

type UserModField struct {
	obj      *UserModObject
	metadata *core.FieldTypeDef
	modType  ModType
}

var _ Callable = (*UserModField)(nil)

func newModField(ctx context.Context, obj *UserModObject, metadata *core.FieldTypeDef) (*UserModField, error) {
	modType, ok, err := obj.mod.ModTypeFor(ctx, metadata.TypeDef, true)
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
		return f.Call(p.Context, &CallOpts{ParentVal: p.Source})
	}, nil
}

func (f *UserModField) Call(ctx context.Context, callOpts *CallOpts) (any, error) {
	objMap, ok := callOpts.ParentVal.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected object value type %T while calling %s.%s", callOpts.ParentVal, f.obj.typeDef.AsObject.Name, f.metadata.Name)
	}
	return f.modType.ConvertFromSDKResult(ctx, objMap[gqlFieldName(f.metadata.Name)])
}

func (f *UserModField) ReturnType() (ModType, error) {
	return f.modType, nil
}

func (f *UserModField) ArgType(_ string) (ModType, error) {
	return nil, fmt.Errorf("field %s.%s has no arguments", f.obj.typeDef.AsObject.Name, f.metadata.Name)
}
