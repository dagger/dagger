package schema

import (
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
)

// ModType wraps the core TypeDef type with schema specific concerns like ID conversion
// and tracking of the module in which the type was originally defined.
type ModType interface {
	// ConvertFromSDKResult converts a value returned from an SDK into values expected by the server,
	// including conversion of IDs to their "unpacked" objects
	ConvertFromSDKResult(ctx context.Context, value any) (any, error)
	// ConvertToSDKInput converts a value from the server into a value expected by the SDK, which may
	// include converting objects to their IDs
	ConvertToSDKInput(ctx context.Context, value any) (any, error)
	// SourceMod is the module in which this type was originally defined
	SourceMod() Mod
}

// PrimitiveType are the basic types like string, int, bool, void, etc.
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

// CoreModObject represents objects from core (Container, Directory, etc.)
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

// UserModObject is an object defined by a user module
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

		for k, v := range value {
			normalizedName := gqlFieldName(k)
			field, ok, err := obj.FieldByName(ctx, normalizedName)
			if err != nil {
				return nil, fmt.Errorf("failed to get field %q: %w", k, err)
			}
			if !ok {
				continue
			}

			value[k], err = field.modType.ConvertToSDKInput(ctx, v)
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
	returnType, ok, err := mod.ModTypeFor(ctx, metadata.ReturnType, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get mod type for function %q return type: %w", metadata.Name, err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to find mod type for function %q return type", metadata.Name)
	}

	argTypes := make(map[string]*UserModFunctionArg, len(metadata.Args))
	for _, argMetadata := range metadata.Args {
		argModType, ok, err := mod.ModTypeFor(ctx, argMetadata.TypeDef, true)
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
		// serialize the parentVal so it can be added to the cache key
		parentBytes, err := json.Marshal(parentVal)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal parent value: %w", err)
		}
		callerDigestInputs = append(callerDigestInputs, string(parentBytes))

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
		ModuleCallerDigest:            callerDigest,
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
