package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

// indicates an ast field is a "trivial resolver"
// ref: https://graphql.org/learn/execution/#trivial-resolvers
const trivialFieldDirectiveName = "trivialResolveField"

// indicates an ast field is deprecated
const deprecatedDirectiveName = "deprecated"

type ModuleObjectType struct {
	typeDef *ObjectTypeDef
	mod     *Module
}

var _ ModType = &ModuleObjectType{}

func (t *ModuleObjectType) SourceMod() Mod {
	if t.mod == nil {
		return nil
	}
	return t.mod
}

func (t *ModuleObjectType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("ModuleObjectType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case map[string]any:
		return dagql.NewResultForCurrentID(ctx, &ModuleObject{
			Module:  t.mod,
			TypeDef: t.typeDef,
			Fields:  value,
		})
	default:
		return nil, fmt.Errorf("unexpected result value type %T for object %q", value, t.typeDef.Name)
	}
}

func (t *ModuleObjectType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	// NOTE: user mod objects are currently only passed as inputs to the module
	// they originate from; modules can't have inputs/outputs from other modules
	// (other than core). These objects are also passed as their direct json
	// serialization rather than as an ID (so that SDKs can decode them without
	// needing to make calls to their own API).
	switch x := value.(type) {
	case DynamicID:
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get current query: %w", err)
		}
		deps, err := query.IDDeps(ctx, x.ID())
		if err != nil {
			return nil, fmt.Errorf("failed to get deps for DynamicID: %w", err)
		}
		dag, err := deps.Schema(ctx)
		if err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		val, err := dag.Load(ctx, x.ID())
		if err != nil {
			return nil, fmt.Errorf("load DynamicID: %w", err)
		}
		switch x := val.(type) {
		case dagql.ObjectResult[*ModuleObject]:
			return x.Self().Fields, nil
		case dagql.ObjectResult[*InterfaceAnnotatedValue]:
			return x.Self().Fields, nil
		default:
			return nil, fmt.Errorf("unexpected value type %T", x)
		}
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle %T", t, x)
	}
}

func (t *ModuleObjectType) CollectCoreIDs(ctx context.Context, value dagql.AnyResult, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}
	var objFields map[string]any
	switch value := value.Unwrap().(type) {
	case nil:
		return nil
	case *ModuleObject:
		objFields = value.Fields
	case *InterfaceAnnotatedValue:
		objFields = value.Fields
	default:
		return fmt.Errorf("expected *ModuleObject, got %T", value)
	}

	for k, v := range objFields {
		fieldTypeDef, ok := t.typeDef.FieldByOriginalName(k)
		if !ok {
			// if this is a private field, then we still should do best-effort collection
			unknownCollectIDs(v, ids)
			continue
		}
		modType, ok, err := t.mod.ModTypeFor(ctx, fieldTypeDef.TypeDef, true)
		if err != nil {
			return fmt.Errorf("failed to get mod type for field %q: %w", k, err)
		}
		if !ok {
			return fmt.Errorf("could not find mod type for field %q", k)
		}

		curID := value.ID()
		fieldID := curID.Append(
			fieldTypeDef.TypeDef.ToType(),
			fieldTypeDef.Name,
			curID.View(),
			curID.Module(),
			0,
			"",
		)
		ctx := dagql.ContextWithID(ctx, fieldID)

		typed, err := modType.ConvertFromSDKResult(ctx, v)
		if err != nil {
			return fmt.Errorf("failed to convert field %q: %w", k, err)
		}
		if err := modType.CollectCoreIDs(ctx, typed, ids); err != nil {
			return fmt.Errorf("failed to collect IDs for field %q: %w", k, err)
		}
	}

	return nil
}

// unknownCollectIDs naively walks a json-decoded value from a module object
// type, and tries to find *any* IDs that *might* be found
func unknownCollectIDs(value any, ids map[digest.Digest]*resource.ID) {
	switch value := value.(type) {
	case nil:
		return
	case string:
		var idp call.ID
		if err := idp.Decode(value); err != nil {
			return
		}
		ids[idp.Digest()] = &resource.ID{
			ID:       idp,
			Optional: true, // mark this id as optional, since it's a best-guess attempt
		}
	case []any:
		for _, value := range value {
			unknownCollectIDs(value, ids)
		}
	case map[string]any:
		for _, value := range value {
			unknownCollectIDs(value, ids)
		}
	}
}

func (t *ModuleObjectType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind:     TypeDefKindObject,
		AsObject: dagql.NonNull(t.typeDef),
	}
}

type Callable interface {
	Call(context.Context, *CallOpts) (dagql.AnyResult, error)
	ReturnType() (ModType, error)
	ArgType(argName string) (ModType, error)
	CacheConfigForCall(context.Context, dagql.AnyResult, map[string]dagql.Input, dagql.View, dagql.CacheConfig) (*dagql.CacheConfig, error)
}

func (t *ModuleObjectType) GetCallable(ctx context.Context, name string) (Callable, error) {
	mod := t.mod
	if field, ok := t.typeDef.FieldByName(name); ok {
		fieldType, ok, err := mod.ModTypeFor(ctx, field.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("get field return type: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("could not find type for field type: %s", field.TypeDef.ToType())
		}
		return &CallableField{
			Module: t.mod,
			Field:  field,
			Return: fieldType,
		}, nil
	}
	if fun, ok := t.typeDef.FunctionByName(name); ok {
		return NewModFunction(
			ctx,
			mod,
			t.typeDef,
			mod.Runtime.Value,
			fun,
		)
	}
	return nil, fmt.Errorf("no field or function %q found on object %q", name, t.typeDef.Name)
}

type ModuleObject struct {
	Module *Module

	TypeDef *ObjectTypeDef
	Fields  map[string]any
}

func (obj *ModuleObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: obj.TypeDef.Name,
		NonNull:   true,
	}
}

var _ HasPBDefinitions = (*ModuleObject)(nil)

func (obj *ModuleObject) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	defs := []*pb.Definition{}
	objDef := obj.TypeDef
	for _, field := range objDef.Fields {
		// TODO: we skip over private fields, we can't convert them anyways (this is a bug)
		name := field.OriginalName
		val, ok := obj.Fields[name]
		if !ok {
			// missing field
			continue
		}
		fieldType, ok, err := obj.Module.ModTypeFor(ctx, field.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for field %q: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for field %q", name)
		}

		curID := dagql.CurrentID(ctx)
		fieldID := curID.Append(
			field.TypeDef.ToType(),
			field.Name,
			curID.View(),
			curID.Module(),
			0,
			"",
		)
		ctx := dagql.ContextWithID(ctx, fieldID)

		converted, err := fieldType.ConvertFromSDKResult(ctx, val)
		if err != nil {
			return nil, fmt.Errorf("failed to convert field %q: %w", name, err)
		}
		if converted == nil {
			continue
		}
		fieldDefs, err := collectPBDefinitions(ctx, converted.Unwrap())
		if err != nil {
			return nil, err
		}
		defs = append(defs, fieldDefs...)
	}
	return defs, nil
}

func (obj *ModuleObject) TypeDescription() string {
	return formatGqlDescription(obj.TypeDef.Description)
}

func (obj *ModuleObject) TypeDefinition(view dagql.View) *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Object,
		Name: obj.Type().Name(),
	}
	if obj.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, obj.TypeDef.SourceMap.Value.TypeDirective())
	}
	return def
}

func (obj *ModuleObject) Install(ctx context.Context, dag *dagql.Server) error {
	if obj.Module.ResultID == nil {
		return fmt.Errorf("installing object %q too early", obj.TypeDef.Name)
	}

	class := dagql.NewClass(dag, dagql.ClassOpts[*ModuleObject]{
		Typed: obj,
	})
	objDef := obj.TypeDef
	mod := obj.Module
	if gqlObjectName(objDef.OriginalName) == gqlObjectName(mod.OriginalName) {
		if err := obj.installConstructor(ctx, dag); err != nil {
			return fmt.Errorf("failed to install constructor: %w", err)
		}
	}
	fields := obj.fields()

	funs, err := obj.functions(ctx, dag)
	if err != nil {
		return err
	}
	fields = append(fields, funs...)

	class.Install(fields...)
	dag.InstallObject(class)

	return nil
}

func (obj *ModuleObject) installConstructor(ctx context.Context, dag *dagql.Server) error {
	objDef := obj.TypeDef
	mod := obj.Module

	// if no constructor defined, install a basic one that initializes an empty object
	if !objDef.Constructor.Valid {
		spec := dagql.FieldSpec{
			Name: gqlFieldName(mod.Name()),
			// Description: "TODO", // XXX(vito)
			Type:   obj,
			Module: obj.Module.IDModule(),
		}

		if objDef.SourceMap.Valid {
			spec.Directives = append(spec.Directives, objDef.SourceMap.Value.TypeDirective())
		}

		dag.Root().ObjectType().Extend(
			spec,
			func(ctx context.Context, self dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentID(ctx, &ModuleObject{
					Module:  mod,
					TypeDef: objDef,
					Fields:  map[string]any{},
				})
			},
			dagql.CacheSpec{
				GetCacheConfig: mod.CacheConfigForCall,
			},
		)
		return nil
	}

	// use explicit user-defined constructor if provided
	fnTypeDef := objDef.Constructor.Value
	if fnTypeDef.ReturnType.Kind != TypeDefKindObject {
		return fmt.Errorf("constructor function for object %s must return that object", objDef.OriginalName)
	}
	if fnTypeDef.ReturnType.AsObject.Value.OriginalName != objDef.OriginalName {
		return fmt.Errorf("constructor function for object %s must return that object", objDef.OriginalName)
	}

	fn, err := NewModFunction(ctx, mod, objDef, mod.Runtime.Value, fnTypeDef)
	if err != nil {
		return fmt.Errorf("failed to create function: %w", err)
	}

	spec, err := fn.metadata.FieldSpec(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to get field spec: %w", err)
	}
	spec.Name = gqlFieldName(mod.Name())
	spec.Module = obj.Module.IDModule()

	dag.Root().ObjectType().Extend(
		spec,
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			var callInput []CallInput
			for k, v := range args {
				callInput = append(callInput, CallInput{
					Name:  k,
					Value: v,
				})
			}
			return fn.Call(ctx, &CallOpts{
				Inputs:       callInput,
				ParentTyped:  nil,
				ParentFields: nil,
				Cache:        dagql.IsInternal(ctx),
				Server:       dag,
			})
		},
		dagql.CacheSpec{
			GetCacheConfig: fn.CacheConfigForCall,
		},
	)

	return nil
}

func (obj *ModuleObject) fields() (fields []dagql.Field[*ModuleObject]) {
	for _, field := range obj.TypeDef.Fields {
		fields = append(fields, objField(obj.Module, field))
	}
	return
}

func (obj *ModuleObject) functions(ctx context.Context, dag *dagql.Server) (fields []dagql.Field[*ModuleObject], err error) {
	objDef := obj.TypeDef
	for _, fun := range obj.TypeDef.Functions {
		objFun, err := objFun(ctx, obj.Module, objDef, fun, dag)
		if err != nil {
			return nil, err
		}
		fields = append(fields, objFun)
	}
	return
}

func objField(mod *Module, field *FieldTypeDef) dagql.Field[*ModuleObject] {
	spec := &dagql.FieldSpec{
		Name:        field.Name,
		Description: field.Description,
		Type:        field.TypeDef.ToTyped(),
		Module:      mod.IDModule(),
	}
	spec.Directives = append(spec.Directives, &ast.Directive{
		Name: trivialFieldDirectiveName,
	})
	if field.SourceMap.Valid {
		spec.Directives = append(spec.Directives, field.SourceMap.Value.TypeDirective())
	}
	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view dagql.View) (dagql.AnyResult, error) {
			modType, ok, err := mod.ModTypeFor(ctx, field.TypeDef, true)
			if err != nil {
				return nil, fmt.Errorf("failed to get mod type for field %q: %w", field.Name, err)
			}
			if !ok {
				return nil, fmt.Errorf("could not find mod type for field %q", field.Name)
			}
			fieldVal, found := obj.Self().Fields[field.OriginalName]
			if !found {
				// the field *might* not have been set yet on the object (even
				// though the typedef has it) - so just pick a suitable zero value
				fieldVal = nil
			}
			return modType.ConvertFromSDKResult(ctx, fieldVal)
		},
		CacheSpec: dagql.CacheSpec{
			GetCacheConfig: mod.CacheConfigForCall,
		},
	}
}

func objFun(ctx context.Context, mod *Module, objDef *ObjectTypeDef, fun *Function, dag *dagql.Server) (dagql.Field[*ModuleObject], error) {
	var f dagql.Field[*ModuleObject]
	modFun, err := NewModFunction(
		ctx,
		mod,
		objDef,
		mod.Runtime.Value,
		fun,
	)
	if err != nil {
		return f, fmt.Errorf("failed to create function %q: %w", fun.Name, err)
	}
	spec, err := fun.FieldSpec(ctx, mod)
	if err != nil {
		return f, fmt.Errorf("failed to get field spec: %w", err)
	}
	spec.Module = mod.IDModule()

	return dagql.Field[*ModuleObject]{
		Spec: &spec,
		Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view dagql.View) (dagql.AnyResult, error) {
			opts := &CallOpts{
				ParentTyped:  obj,
				ParentFields: obj.Self().Fields,
				// TODO: there may be a more elegant way to do this, but the desired
				// effect is to cache SDK module calls, which we used to do pre-DagQL.
				// We should figure out how user modules can opt in to caching, too.
				Cache:          dagql.IsInternal(ctx),
				SkipSelfSchema: false,
				Server:         dag,
			}
			for name, val := range args {
				opts.Inputs = append(opts.Inputs, CallInput{
					Name:  name,
					Value: val,
				})
			}
			// NB: ensure deterministic order
			sort.Slice(opts.Inputs, func(i, j int) bool {
				return opts.Inputs[i].Name < opts.Inputs[j].Name
			})
			return modFun.Call(ctx, opts)
		},
		CacheSpec: dagql.CacheSpec{
			GetCacheConfig: modFun.CacheConfigForCall,
		},
	}, nil
}

type CallableField struct {
	Module *Module
	Field  *FieldTypeDef
	Return ModType
}

var _ Callable = &CallableField{}

func (f *CallableField) Call(ctx context.Context, opts *CallOpts) (dagql.AnyResult, error) {
	val, ok := opts.ParentFields[f.Field.OriginalName]
	if !ok {
		return nil, fmt.Errorf("field %q not found on object %q", f.Field.Name, opts.ParentFields)
	}
	typed, err := f.Return.ConvertFromSDKResult(ctx, val)
	if err != nil {
		return nil, fmt.Errorf("failed to convert field %q: %w", f.Field.Name, err)
	}
	return typed, nil
}

func (f *CallableField) ReturnType() (ModType, error) {
	return f.Return, nil
}

func (f *CallableField) ArgType(argName string) (ModType, error) {
	return nil, fmt.Errorf("field cannot have argument %q", argName)
}

func (f *CallableField) CacheConfigForCall(
	ctx context.Context,
	parent dagql.AnyResult,
	args map[string]dagql.Input,
	view dagql.View,
	inputCfg dagql.CacheConfig,
) (*dagql.CacheConfig, error) {
	return f.Module.CacheConfigForCall(ctx, parent, args, view, inputCfg)
}
