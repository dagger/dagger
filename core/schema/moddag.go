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

func (d *ModDag) Schema(ctx context.Context) (ExecutableSchema, error) {
	var executableSchemas []ExecutableSchema
	for _, root := range d.roots {
		schema, err := root.Schema(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get schema for root %q: %w", root.Name(), err)
		}
		executableSchemas = append(executableSchemas, schema)
	}
	return mergeExecutableSchemas(nil, executableSchemas...)
}

func (d *ModDag) SchemaIntrospectionJSON(ctx context.Context) (string, error) {
	executableSchema, err := d.Schema(ctx)
	if err != nil {
		return "", err
	}
	compiledSchema, err := compile(executableSchema)
	if err != nil {
		return "", err
	}
	return schemaIntrospectionJSON(ctx, *compiledSchema)
}

func (d *ModDag) ObjectByName(ctx context.Context, objName string) (ModObject, bool, error) {
	for _, root := range d.roots {
		obj, ok, err := root.ObjectByName(ctx, objName)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get object for root %q: %w", root.Name(), err)
		}
		if ok {
			return obj, true, nil
		}
	}
	return nil, false, nil
}

/* TODO: rm if unused
func (d *ModDag) Walk(cb func(Mod) error) error {
	memo := make(map[string]struct{})
	currentMods := d.roots
	for len(currentMods) > 0 {
		var nextMods []Mod
		for _, mod := range currentMods {
			if _, ok := memo[mod.Name()]; ok {
				continue
			}
			memo[mod.Name()] = struct{}{}
			if err := cb(mod); err != nil {
				return err
			}
			nextMods = append(nextMods, mod.Dependencies()...)
		}
		currentMods = nextMods
	}
	return nil
}

func (d *ModDag) TopologicalSort() []Mod {
	// TODO: cache? or maybe just have callers cache as needed

	modsByName := make(map[string]Mod)
	modDeps := make(map[string]map[string]struct{})
	reverseModDeps := make(map[string]map[string]struct{})
	var currentMods []Mod
	d.Walk(func(mod Mod) error {
		modsByName[mod.Name()] = mod
		if len(mod.Dependencies()) == 0 {
			currentMods = append(currentMods, mod)
			return nil
		}
		modDeps[mod.Name()] = make(map[string]struct{})
		for _, dep := range mod.Dependencies() {
			modDeps[mod.Name()][dep.Name()] = struct{}{}
			reverseDeps, ok := reverseModDeps[dep.Name()]
			if !ok {
				reverseDeps = make(map[string]struct{})
				reverseModDeps[dep.Name()] = reverseDeps
			}
			reverseDeps[mod.Name()] = struct{}{}
		}
		return nil
	})

	var sorted []Mod
	for len(currentMods) > 0 {
		sort.Slice(currentMods, func(i, j int) bool {
			return currentMods[i].Name() < currentMods[j].Name()
		})
		sorted = append(sorted, currentMods...)

		var nextMods []Mod
		for _, mod := range currentMods {
			for dep := range reverseModDeps[mod.Name()] {
				delete(modDeps[dep], mod.Name())
				if len(modDeps[dep]) == 0 {
					nextMods = append(nextMods, modsByName[dep])
				}
			}
		}
		currentMods = nextMods
	}

	return sorted
}
*/

type Mod interface {
	Name() string
	DagDigest() digest.Digest
	Dependencies() []Mod
	Schema(context.Context) (ExecutableSchema, error)
	DependencySchemaIntrospectionJSON(context.Context) (string, error)
	ObjectByName(ctx context.Context, objName string) (ModObject, bool, error)
}

type ModObject interface {
	SourceMod() Mod
	FromID(id string) (any, error)
}

type CoreMod struct {
	executableSchema  ExecutableSchema
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

func (m *CoreMod) Schema(_ context.Context) (ExecutableSchema, error) {
	return m.executableSchema, nil
}

func (m *CoreMod) DependencySchemaIntrospectionJSON(_ context.Context) (string, error) {
	return m.introspectionJSON, nil
}

func (m *CoreMod) ObjectByName(_ context.Context, objName string) (ModObject, bool, error) {
	objName = gqlObjectName(objName)
	resolver, ok := m.executableSchema.Resolvers()[objName]
	if !ok {
	}
	idableResolver, ok := resolver.(IDableObjectResolver)
	if !ok {
		return nil, false, nil
	}
	return &CoreModObject{coreMod: m, resolver: idableResolver}, true, nil
}

type CoreModObject struct {
	coreMod  *CoreMod
	resolver IDableObjectResolver
}

var _ ModObject = (*CoreModObject)(nil)

func (obj *CoreModObject) SourceMod() Mod {
	return obj.coreMod
}

func (obj *CoreModObject) FromID(id string) (any, error) {
	return obj.resolver.FromID(id)
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

func (m *UserMod) MainModuleObject(ctx context.Context) (*UserModObject, error) {
	objs, err := m.Objects(ctx)
	if err != nil {
		return nil, err
	}
	mainObjName := gqlObjectName(m.metadata.Name)
	for _, obj := range objs {
		if obj.typeDef.AsObject.Name == mainObjName {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("failed to find main module object %q", mainObjName)
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
	result, err := newModFunction(m, nil, runtime, core.NewFunction("", &core.TypeDef{
		Kind:     core.TypeDefKindObject,
		AsObject: core.NewObjectTypeDef("Module", ""),
	})).Call(ctx, true, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call module %q to get functions: %w", m.Name, err)
	}

	modMeta, ok := result.(*core.Module)
	if !ok {
		return nil, fmt.Errorf("expected ModuleMetadata result, got %T", result)
	}

	var objs []*UserModObject
	for _, objTypeDef := range modMeta.Objects {
		obj, err := newModObject(ctx, m, objTypeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to create object: %w", err)
		}

		if err := m.validateTypeDef(ctx, objTypeDef); err != nil {
			return nil, fmt.Errorf("failed to validate type def: %w", err)
		}

		// namespace the module objects
		if err := m.namespaceTypeDef(ctx, objTypeDef); err != nil {
			return nil, fmt.Errorf("failed to namespace type def: %w", err)
		}

		objs = append(objs, obj)
	}
	return objs, nil
}

func (m *UserMod) Schema(ctx context.Context) (ExecutableSchema, error) {
	objs, err := m.Objects(ctx)
	if err != nil {
		return nil, err
	}

	executableSchemas := make([]ExecutableSchema, 0, len(objs))
	for _, obj := range objs {
		objSchemaDoc, objResolvers, err := obj.Schema(ctx)
		if err != nil {
			return nil, err
		}
		buf := &bytes.Buffer{}
		formatter.NewFormatter(buf).FormatSchemaDocument(objSchemaDoc)
		typeSchemaStr := buf.String()

		executableSchema := StaticSchema(StaticSchemaParams{
			Name:      fmt.Sprintf("%s.%s", m.metadata.Name, obj.typeDef.AsObject.Name),
			Schema:    typeSchemaStr,
			Resolvers: objResolvers,
		})
		executableSchemas = append(executableSchemas, executableSchema)
	}

	return mergeExecutableSchemas(nil, executableSchemas...)
}

func (m *UserMod) DependencySchemaIntrospectionJSON(ctx context.Context) (string, error) {
	return m.deps.SchemaIntrospectionJSON(ctx)
}

func (m *UserMod) ObjectByName(ctx context.Context, objName string) (ModObject, bool, error) {
	objName = gqlObjectName(objName)

	objs, err := m.Objects(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, obj := range objs {
		if gqlObjectName(obj.typeDef.AsObject.Name) == objName {
			return obj, true, nil
		}
	}
	return nil, false, nil
}

func (m *UserMod) validateTypeDef(ctx context.Context, typeDef *core.TypeDef) error {
	switch typeDef.Kind {
	case core.TypeDefKindList:
		return m.validateTypeDef(ctx, typeDef.AsList.ElementTypeDef)
	case core.TypeDefKindObject:
		obj := typeDef.AsObject
		baseObjName := gqlObjectName(obj.Name)

		// check whether this is a pre-existing object from core or another module
		_, ok, err := m.deps.ObjectByName(ctx, baseObjName)
		if err != nil {
			return fmt.Errorf("failed to get object for type def: %w", err)
		}
		if ok {
			// already validated, skip
			return nil
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
		_, preExistingObject, err := m.deps.ObjectByName(ctx, obj.Name)
		if err != nil {
			return fmt.Errorf("failed to get object for type def: %w", err)
		}
		if !preExistingObject {
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

// TODO: to support linkDependencyBlobs, this needs to be recursive maybe?
func (m *UserMod) convertValueIDs(ctx context.Context, value any, resultTypeDef *core.TypeDef) (any, error) {
	switch resultTypeDef.Kind {
	case core.TypeDefKindObject:
		id, ok := value.(string)
		if !ok {
			return value, nil
		}

		// This might be wrong, you also want to handle objects from this module and from core, but not other deps
		obj, ok, err := m.ObjectByName(ctx, resultTypeDef.AsObject.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get object for type def: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find object %q", resultTypeDef.AsObject.Name)
		}
		return obj.FromID(id)
	case core.TypeDefKindList:
		list, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("expected list, got %T", value)
		}
		resultList := make([]any, len(list))
		for i, item := range list {
			var err error
			resultList[i], err = m.convertValueIDs(ctx, item, resultTypeDef.AsList.ElementTypeDef)
			if err != nil {
				return nil, err
			}
		}
		return resultList, nil
	default:
		return value, nil
	}
}

type UserModObject struct {
	api       *APIServer
	mod       *UserMod
	typeDef   *core.TypeDef
	runtime   *core.Container
	fields    []*UserModField
	functions []*UserModFunction
}

var _ ModObject = (*UserModObject)(nil)

func newModObject(ctx context.Context, mod *UserMod, typeDef *core.TypeDef) (*UserModObject, error) {
	if typeDef.Kind != core.TypeDefKindObject {
		return nil, fmt.Errorf("expected object type def, got %s", typeDef.Kind)
	}
	obj := &UserModObject{
		api:     mod.api,
		mod:     mod,
		typeDef: typeDef,
	}
	for _, field := range typeDef.AsObject.Fields {
		obj.fields = append(obj.fields, newModField(obj, field))
	}
	runtime, err := mod.Runtime(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module runtime: %w", err)
	}
	for _, fn := range typeDef.AsObject.Functions {
		obj.functions = append(obj.functions, newModFunction(mod, obj, runtime, fn))
	}
	return obj, nil
}

func (obj *UserModObject) SourceMod() Mod {
	return obj.mod
}

func (obj *UserModObject) FromID(id string) (any, error) {
	return resourceid.DecodeModuleID(id, obj.typeDef.AsObject.Name)
}

func (obj *UserModObject) FunctionByName(name string) (*UserModFunction, error) {
	name = gqlFieldName(name)
	for _, fn := range obj.functions {
		if gqlFieldName(fn.typeDef.Name) == name {
			return fn, nil
		}
	}
	return nil, fmt.Errorf("failed to find function %q", name)
}

func (obj *UserModObject) Schema(ctx context.Context) (*ast.SchemaDocument, Resolvers, error) {
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
	_, preExistingObject, err := obj.mod.deps.ObjectByName(ctx, objName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get object for type def: %w", err)
	}
	if preExistingObject {
		// modules can reference types from core/other modules as types, but they
		// can't attach any new fields or functions to them
		if len(objTypeDef.Fields) > 0 || len(objTypeDef.Functions) > 0 {
			return nil, nil, fmt.Errorf("cannot attach new fields or functions to object %q from outside module", objName)
		}
		return nil, nil, nil
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

	for _, field := range obj.fields {
		fieldDef, resolver, err := field.Schema(ctx)
		if err != nil {
			return nil, nil, err
		}
		astDef.Fields = append(astDef.Fields, fieldDef)
		newObjResolver[fieldDef.Name] = resolver
	}

	for _, fn := range obj.functions {
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
			return obj.mod.convertValueIDs(ctx, p.Args["id"], obj.typeDef)
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
			fn := newModFunction(obj.mod, obj, runtime, fnTypeDef)

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
	obj     *UserModObject
	typeDef *core.FieldTypeDef
}

func newModField(obj *UserModObject, typeDef *core.FieldTypeDef) *UserModField {
	return &UserModField{
		obj:     obj,
		typeDef: typeDef,
	}
}

func (f *UserModField) Schema(ctx context.Context) (*ast.FieldDefinition, graphql.FieldResolveFn, error) {
	fieldASTType, err := typeDefToASTType(f.typeDef.TypeDef, false)
	if err != nil {
		return nil, nil, err
	}

	// Check if this is a type from another (non-core) module, which is currently
	// not allowed
	if f.typeDef.TypeDef.Kind == core.TypeDefKindObject {
		obj, ok, err := f.obj.mod.deps.ObjectByName(ctx, fieldASTType.Name())
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get object for type def: %w", err)
		}
		if ok && obj.SourceMod().Name() != coreModuleName {
			return nil, nil, fmt.Errorf("object %q field %q cannot reference external type from dependency module %q",
				f.obj.typeDef.AsObject.OriginalName,
				f.typeDef.OriginalName,
				obj.SourceMod().Name(),
			)
		}
	}

	fieldDef := &ast.FieldDefinition{
		Name:        f.typeDef.Name,
		Description: formatGqlDescription(f.typeDef.Description),
		Type:        fieldASTType,
	}
	return fieldDef, func(p graphql.ResolveParams) (any, error) {
		p.Info.FieldName = f.typeDef.OriginalName
		res, err := graphql.DefaultResolveFn(p)
		if err != nil {
			return nil, err
		}
		return f.obj.mod.convertValueIDs(ctx, res, f.typeDef.TypeDef)
	}, nil
}

type UserModFunction struct {
	api     *APIServer
	mod     *UserMod
	obj     *UserModObject // may be nil for special functions like the module definition function call
	typeDef *core.Function
	runtime *core.Container
}

func newModFunction(mod *UserMod, obj *UserModObject, runtime *core.Container, typeDef *core.Function) *UserModFunction {
	return &UserModFunction{
		api:     mod.api,
		mod:     mod,
		obj:     obj,
		typeDef: typeDef,
		runtime: runtime,
	}
}

func (fn *UserModFunction) Digest() digest.Digest {
	inputs := []string{
		fn.mod.DagDigest().String(),
		fn.typeDef.Name,
	}
	if fn.obj != nil {
		inputs = append(inputs, fn.obj.typeDef.AsObject.Name)
	}
	return digest.FromString(strings.Join(inputs, " "))
}

func (fn *UserModFunction) Schema(ctx context.Context) (*ast.FieldDefinition, graphql.FieldResolveFn, error) {
	fnName := gqlFieldName(fn.typeDef.Name)
	var objFnName string
	if fn.obj != nil {
		objFnName = fmt.Sprintf("%s.%s", fn.obj.typeDef.AsObject.Name, fnName)
	} else {
		objFnName = fnName
	}

	returnASTType, err := typeDefToASTType(fn.typeDef.ReturnType, false)
	if err != nil {
		return nil, nil, err
	}

	// Check if this is a type from another (non-core) module, which is currently
	// not allowed
	if fn.typeDef.ReturnType.Kind == core.TypeDefKindObject {
		obj, ok, err := fn.mod.deps.ObjectByName(ctx, returnASTType.Name())
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get object for type def: %w", err)
		}
		if ok && obj.SourceMod().Name() != coreModuleName {
			var objName string
			if fn.obj != nil {
				objName = fn.obj.typeDef.AsObject.OriginalName
			}
			return nil, nil, fmt.Errorf("object %q function %q cannot return external type from dependency module %q",
				objName,
				fn.typeDef.OriginalName,
				obj.SourceMod().Name(),
			)
		}
	}

	fieldDef := &ast.FieldDefinition{
		Name:        fnName,
		Description: formatGqlDescription(fn.typeDef.Description),
		Type:        returnASTType,
	}

	// graphql arg name -> arg type
	argsByName := make(map[string]*core.FunctionArg, len(fn.typeDef.Args))

	for _, fnArg := range fn.typeDef.Args {
		argASTType, err := typeDefToASTType(fnArg.TypeDef, true)
		if err != nil {
			return nil, nil, err
		}

		// Check if this is a type from another (non-core) module, which is currently
		// not allowed
		if fnArg.TypeDef.Kind == core.TypeDefKindObject {
			obj, ok, err := fn.mod.deps.ObjectByName(ctx, argASTType.Name())
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get object for type def: %w", err)
			}
			if ok && obj.SourceMod().Name() != coreModuleName {
				var objName string
				if fn.obj != nil {
					objName = fn.obj.typeDef.AsObject.OriginalName
				}
				return nil, nil, fmt.Errorf("object %q function %q arg %q cannot reference external type from dependency module %q",
					objName,
					fn.typeDef.OriginalName,
					fnArg.OriginalName,
					obj.SourceMod().Name(),
				)
			}
		}

		defaultValue, err := astDefaultValue(fnArg.TypeDef, fnArg.DefaultValue)
		if err != nil {
			return nil, nil, err
		}
		argDef := &ast.ArgumentDefinition{
			Name:         gqlArgName(fnArg.Name),
			Description:  formatGqlDescription(fnArg.Description),
			Type:         argASTType,
			DefaultValue: defaultValue,
		}
		fieldDef.Arguments = append(fieldDef.Arguments, argDef)
		argsByName[argDef.Name] = fnArg
	}

	resolver := ToResolver(func(ctx context.Context, parent any, args map[string]any) (_ any, rerr error) {
		defer func() {
			if r := recover(); r != nil {
				rerr = fmt.Errorf("panic in %s: %s %s", objFnName, r, string(debug.Stack()))
			}
		}()
		if fn.obj != nil {
			var err error
			parent, err = fn.mod.convertValueIDs(ctx, parent, fn.obj.typeDef)
			if err != nil {
				return nil, fmt.Errorf("failed to convert parent: %w", err)
			}
		}

		var callInput []*core.CallInput
		for k, v := range args {
			argType, ok := argsByName[k]
			if !ok {
				bklog.G(ctx).Warnf("unknown arg %q for function %q", k, objFnName)
				continue
			}
			v, err := fn.mod.convertValueIDs(ctx, v, argType.TypeDef)
			if err != nil {
				return nil, fmt.Errorf("failed to convert arg %q: %w", k, err)
			}

			callInput = append(callInput, &core.CallInput{
				Name:  argType.OriginalName,
				Value: v,
			})
		}
		result, err := fn.Call(ctx, false, nil, parent, callInput)
		if err != nil {
			return nil, fmt.Errorf("failed to call function %q: %w", objFnName, err)
		}
		return result, nil
	})

	return fieldDef, resolver, nil
}

func (fn *UserModFunction) Call(ctx context.Context, cache bool, pipeline pipeline.Path, parentVal any, args []*core.CallInput) (any, error) {
	cacheKeys := []string{fn.Digest().String()}

	for _, arg := range args {
		argDgst, err := arg.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get arg digest: %w", err)
		}
		cacheKeys = append(cacheKeys, argDgst.String())
	}

	if !cache {
		// [shykes] inject a cachebuster before runtime exec,
		// to fix crippling mandatory memoization of all functions.
		// [sipsma] use the ServerID so that we only bust once-per-session and thus avoid exponential runtime complexity
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		cacheKeys = append(cacheKeys, clientMetadata.ServerID)
	}

	cacheKey := digest.FromString(strings.Join(cacheKeys, " "))

	ctr := fn.runtime

	metaDir := core.NewScratchDirectory(pipeline, fn.api.platform)
	ctr, err := ctr.WithMountedDirectory(ctx, fn.api.bk, modMetaDirPath, metaDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod metadata directory: %w", err)
	}

	// Setup the Exec for the Function call and evaluate it
	ctr, err = ctr.WithExec(ctx, fn.api.bk, fn.api.progSockPath, fn.api.platform, core.ContainerExecOpts{
		// TODO: rename ModuleContextDigest
		ModuleContextDigest:           cacheKey,
		ExperimentalPrivilegedNesting: true,
		NestedInSameSession:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec function: %w", err)
	}

	ctrOutputDir, err := ctr.Directory(ctx, fn.api.bk, fn.api.services, modMetaDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get function output directory: %w", err)
	}

	callMeta := &core.FunctionCall{
		Name:      fn.typeDef.OriginalName,
		Parent:    parentVal,
		InputArgs: args,
	}
	if fn.obj != nil {
		callMeta.ParentName = fn.obj.typeDef.AsObject.OriginalName
	}

	err = fn.api.RegisterFunctionCall(cacheKey, fn.mod, callMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to register function call: %w", err)
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

	returnValue, err = fn.mod.convertValueIDs(ctx, returnValue, fn.typeDef.ReturnType)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return value: %w", err)
	}

	if err := fn.linkDependencyBlobs(ctx, result, returnValue, fn.typeDef.ReturnType); err != nil {
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
