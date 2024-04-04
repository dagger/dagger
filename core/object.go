package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

type ModuleObjectType struct {
	typeDef *ObjectTypeDef
	mod     *Module
}

func (t *ModuleObjectType) SourceMod() Mod {
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

func (t *ModuleObjectType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind:     TypeDefKindObject,
		AsObject: dagql.NonNull(t.typeDef),
	}
}

type Callable interface {
	Call(context.Context, *call.ID, *CallOpts) (dagql.Typed, error)
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
			mod.InstanceID,
			t.typeDef,
			mod.Runtime,
			fun,
		)
	}
	return nil, fmt.Errorf("no field or function %q found on object %q", name, t.typeDef.Name)
}

type ModuleObject struct {
	Module  *Module
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

	funs, err := obj.functions(ctx)
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

	if !objDef.Constructor.Valid {
		// no constructor defined; install a basic one that initializes an empty
		// object
		dag.Root().ObjectType().Extend(
			dagql.FieldSpec{
				Name: gqlFieldName(mod.Name()),
				// Description: "TODO", // XXX(vito)
				Type:   obj,
				Module: obj.Module.IDModule(),
			},
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

	fn, err := newModFunction(ctx, mod.Query, mod, mod.InstanceID, objDef, mod.Runtime, fnTypeDef)
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
			return fn.Call(ctx, dagql.CurrentID(ctx), &CallOpts{
				Inputs:    callInput,
				ParentVal: nil,
			})
		},
	)

	return nil
}

func (obj *ModuleObject) fields() (fields []dagql.Field[*ModuleObject]) {
	mod := obj.Module
	for _, field := range obj.TypeDef.Fields {
		fields = append(fields, objField(mod, field))
	}
	return
}

func (obj *ModuleObject) functions(ctx context.Context) (fields []dagql.Field[*ModuleObject], err error) {
	objDef := obj.TypeDef
	mod := obj.Module
	for _, fun := range obj.TypeDef.Functions {
		objFun, err := objFun(ctx, mod, objDef, fun)
		if err != nil {
			return nil, err
		}
		fields = append(fields, objFun)
	}
	return
}

func objField(mod *Module, field *FieldTypeDef) dagql.Field[*ModuleObject] {
	return dagql.Field[*ModuleObject]{
		Spec: dagql.FieldSpec{
			Name:        field.Name,
			Description: field.Description,
			Type:        field.TypeDef.ToTyped(),
			Module:      mod.IDModule(),
		},
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
				return nil, fmt.Errorf("field %q not found on object %q", field.Name, obj.Class.TypeName())
			}
			return modType.ConvertFromSDKResult(ctx, fieldVal)
		},
	}
}

func objFun(ctx context.Context, mod *Module, objDef *ObjectTypeDef, fun *Function) (dagql.Field[*ModuleObject], error) {
	var f dagql.Field[*ModuleObject]
	modFun, err := newModFunction(
		ctx,
		mod.Query,
		mod,
		mod.InstanceID,
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
	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, obj dagql.Instance[*ModuleObject], args map[string]dagql.Input) (dagql.Typed, error) {
			opts := &CallOpts{
				ParentVal: obj.Self.Fields,
				// TODO: there may be a more elegant way to do this, but the desired
				// effect is to cache SDK module calls, which we used to do pre-DagQL.
				// We should figure out how user modules can opt in to caching, too.
				Cache: dagql.IsInternal(ctx),
				// Pipeline:  _, // TODO
				SkipSelfSchema: false, // TODO?
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
			return modFun.Call(ctx, dagql.CurrentID(ctx), opts)
		},
	}, nil
}

type CallableField struct {
	Module *Module
	Field  *FieldTypeDef
	Return ModType
}

func (f *CallableField) Call(ctx context.Context, id *call.ID, opts *CallOpts) (dagql.Typed, error) {
	val, ok := opts.ParentVal[f.Field.OriginalName]
	if !ok {
		return nil, fmt.Errorf("field %q not found on object %q", f.Field.Name, opts.ParentVal)
	}
	return f.Return.ConvertFromSDKResult(ctx, val)
}

func (f *CallableField) ReturnType() (ModType, error) {
	return f.Return, nil
}

func (f *CallableField) ArgType(argName string) (ModType, error) {
	return nil, fmt.Errorf("field cannot have argument %q", argName)
}
