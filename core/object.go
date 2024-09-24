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

type ModuleObjectType struct {
	typeDef *ObjectTypeDef
	mod     *Module
}

func (t *ModuleObjectType) SourceMod() Mod {
	if t.mod == nil {
		return nil
	}
	return t.mod
}

func (t *ModuleObjectType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("ModuleObjectType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case map[string]any:
		return &ModuleObject{
			Module:  t.mod,
			TypeDef: t.typeDef,
			Fields:  value,
		}, nil
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
		deps, err := t.mod.Query.IDDeps(ctx, x.ID())
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
		case dagql.Instance[*ModuleObject]:
			return x.Self.Fields, nil
		case dagql.Instance[*InterfaceAnnotatedValue]:
			return x.Self.Fields, nil
		default:
			return nil, fmt.Errorf("unexpected value type %T", x)
		}
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle %T", t, x)
	}
}

func (t *ModuleObjectType) CollectCoreIDs(ctx context.Context, value dagql.Typed, ids map[digest.Digest]*resource.ID) error {
	var obj *ModuleObject
	switch value := value.(type) {
	case nil:
		return nil
	case *ModuleObject:
		obj = value
	case dagql.Instance[*ModuleObject]:
		obj = value.Self
	case *InterfaceAnnotatedValue:
		return t.CollectCoreIDs(ctx, &ModuleObject{
			Module:  t.mod,
			TypeDef: t.typeDef,
			Fields:  value.Fields,
		}, ids)
	default:
		return fmt.Errorf("expected *ModuleObject, got %T", value)
	}

	for k, v := range obj.Fields {
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
	Call(context.Context, *CallOpts) (dagql.Typed, error)
	ReturnType() (ModType, error)
	ArgType(argName string) (ModType, error)
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
		return newModFunction(
			ctx,
			mod.Query,
			mod,
			t.typeDef,
			mod.Runtime,
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
	for name, val := range obj.Fields {
		fieldDef, ok := objDef.FieldByOriginalName(name)
		if !ok {
			// TODO: must be a private field; skip, since we can't convert it anyhow.
			// (this is a bug)
			continue
		}
		fieldType, ok, err := obj.Module.ModTypeFor(ctx, fieldDef.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for field %q: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for field %q", name)
		}
		converted, err := fieldType.ConvertFromSDKResult(ctx, val)
		if err != nil {
			return nil, fmt.Errorf("failed to convert field %q: %w", name, err)
		}
		fieldDefs, err := collectPBDefinitions(ctx, converted)
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

func (obj *ModuleObject) TypeDefinition(views ...string) *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Object,
		Name: obj.Type().Name(),
	}
	if obj.TypeDef.SourceMap != nil {
		def.Directives = append(def.Directives, obj.TypeDef.SourceMap.TypeDirective())
	}
	return def
}

func (obj *ModuleObject) Install(ctx context.Context, dag *dagql.Server) error {
	if obj.Module.InstanceID == nil {
		return fmt.Errorf("installing object %q too early", obj.TypeDef.Name)
	}

	class := dagql.NewClass(dagql.ClassOpts[*ModuleObject]{
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

		if objDef.SourceMap != nil {
			spec.Directives = append(spec.Directives, objDef.SourceMap.TypeDirective())
		}

		dag.Root().ObjectType().Extend(
			spec,
			func(ctx context.Context, self dagql.Object, _ map[string]dagql.Input) (dagql.Typed, error) {
				return &ModuleObject{
					Module:  mod,
					TypeDef: objDef,
					Fields:  map[string]any{},
				}, nil
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

	fn, err := newModFunction(ctx, mod.Query, mod, objDef, mod.Runtime, fnTypeDef)
	if err != nil {
		return fmt.Errorf("failed to create function: %w", err)
	}

	spec, err := fn.metadata.FieldSpec()
	if err != nil {
		return fmt.Errorf("failed to get field spec: %w", err)
	}

	spec.Name = gqlFieldName(mod.Name())

	// NB: functions actually _are_ cached per-session, which matches the
	// lifetime of the server, so we might as well consider them pure.
	// That way there will be locking around concurrent calls, so the user won't
	// see multiple in parallel. Reconsider if/when we have a global cache and/or
	// figure out function caching.
	spec.ImpurityReason = ""

	spec.Module = obj.Module.IDModule()

	if fn.metadata.SourceMap != nil {
		spec.Directives = append(spec.Directives, fn.metadata.SourceMap.TypeDirective())
	}
	for i, arg := range fn.metadata.Args {
		if arg.SourceMap != nil {
			spec.Args[i].Directives = append(spec.Args[i].Directives, arg.SourceMap.TypeDirective())
		}
	}

	dag.Root().ObjectType().Extend(
		spec,
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
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
				Server:       dag,
			})
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
	spec := dagql.FieldSpec{
		Name:        field.Name,
		Description: field.Description,
		Type:        field.TypeDef.ToTyped(),
		Module:      mod.IDModule(),
	}
	if field.SourceMap != nil {
		spec.Directives = append(spec.Directives, field.SourceMap.TypeDirective())
	}
	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, obj dagql.Instance[*ModuleObject], _ map[string]dagql.Input) (dagql.Typed, error) {
			modType, ok, err := mod.ModTypeFor(ctx, field.TypeDef, true)
			if err != nil {
				return nil, fmt.Errorf("failed to get mod type for field %q: %w", field.Name, err)
			}
			if !ok {
				return nil, fmt.Errorf("could not find mod type for field %q", field.Name)
			}
			fieldVal, found := obj.Self.Fields[field.OriginalName]
			if !found {
				// the field *might* not have been set yet on the object (even
				// though the typedef has it) - so just pick a suitable zero value
				fieldVal = nil
			}
			return modType.ConvertFromSDKResult(ctx, fieldVal)
		},
	}
}

func objFun(ctx context.Context, mod *Module, objDef *ObjectTypeDef, fun *Function, dag *dagql.Server) (dagql.Field[*ModuleObject], error) {
	var f dagql.Field[*ModuleObject]
	modFun, err := newModFunction(
		ctx,
		mod.Query,
		mod,
		objDef,
		mod.Runtime,
		fun,
	)
	if err != nil {
		return f, fmt.Errorf("failed to create function %q: %w", fun.Name, err)
	}
	spec, err := fun.FieldSpec()
	if err != nil {
		return f, fmt.Errorf("failed to get field spec: %w", err)
	}
	spec.Module = mod.IDModule()
	if fun.SourceMap != nil {
		spec.Directives = append(spec.Directives, fun.SourceMap.TypeDirective())
	}
	for i, arg := range fun.Args {
		if arg.SourceMap != nil {
			spec.Args[i].Directives = append(spec.Args[i].Directives, arg.SourceMap.TypeDirective())
		}
	}

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, obj dagql.Instance[*ModuleObject], args map[string]dagql.Input) (dagql.Typed, error) {
			opts := &CallOpts{
				ParentTyped:  obj,
				ParentFields: obj.Self.Fields,
				// TODO: there may be a more elegant way to do this, but the desired
				// effect is to cache SDK module calls, which we used to do pre-DagQL.
				// We should figure out how user modules can opt in to caching, too.
				Cache: dagql.IsInternal(ctx),
				// Pipeline:  _, // TODO
				SkipSelfSchema: false, // TODO?
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
	}, nil
}

type CallableField struct {
	Module *Module
	Field  *FieldTypeDef
	Return ModType
}

func (f *CallableField) Call(ctx context.Context, opts *CallOpts) (dagql.Typed, error) {
	val, ok := opts.ParentFields[f.Field.OriginalName]
	if !ok {
		return nil, fmt.Errorf("field %q not found on object %q", f.Field.Name, opts.ParentFields)
	}
	return f.Return.ConvertFromSDKResult(ctx, val)
}

func (f *CallableField) ReturnType() (ModType, error) {
	return f.Return, nil
}

func (f *CallableField) ArgType(argName string) (ModType, error) {
	return nil, fmt.Errorf("field cannot have argument %q", argName)
}
