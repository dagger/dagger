package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

// indicates an ast field is a "trivial resolver"
// ref: https://graphql.org/learn/execution/#trivial-resolvers
const trivialFieldDirectiveName = "trivialResolveField"

type ModuleObjectType struct {
	typeDef *ObjectTypeDef
	mod     dagql.ObjectResult[*Module]
}

var _ ModType = &ModuleObjectType{}

func (t *ModuleObjectType) SourceMod() Mod {
	if t.mod.Self() == nil {
		return nil
	}
	return NewUserMod(t.mod)
}

func (t *ModuleObjectType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("ModuleObjectType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case dagql.AnyResult:
		if value.Type() == nil || value.Type().Name() != t.typeDef.Name {
			return nil, fmt.Errorf("unexpected result value type %T for object %q", value, t.typeDef.Name)
		}
		return value, nil
	case string:
		// A self-call that returns one of the module's own object types
		// serializes the result as the object's GraphQL ID (a string), since
		// the object lives in the module's runtime schema. Decode the ID and
		// load the concrete object, mirroring how InterfaceType handles IDs.
		var id call.ID
		if err := id.Decode(value); err != nil {
			return nil, fmt.Errorf("decode object ID for %q: %w", t.typeDef.Name, err)
		}
		dag, err := CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("current dagql server: %w", err)
		}
		loaded, err := dag.Load(ctx, &id)
		if err != nil {
			return nil, fmt.Errorf("load object ID for %q: %w", t.typeDef.Name, err)
		}
		if loaded.Type() == nil || loaded.Type().Name() != t.typeDef.Name {
			return nil, fmt.Errorf("loaded object type %q does not match expected type %q", loaded.Type().Name(), t.typeDef.Name)
		}
		return loaded, nil
	case map[string]any:
		obj := &ModuleObject{
			Module:  t.mod,
			TypeDef: t.typeDef,
			Fields:  value,
		}
		if err := obj.restoreCollectionBase(ctx); err != nil {
			return nil, err
		}
		res, err := dagql.NewResultForCurrentCall(ctx, obj)
		if err != nil {
			return nil, err
		}
		// Best-effort upgrade to a selectable ObjectResult so the cache
		// preserves the concrete ObjectType across module boundaries; without
		// this it would normalize to Result[Typed] and lose toSelectable info.
		// Falling through if the current server can't see the type is fine —
		// the cache's lazy reconstruction handles cross-module lookup later.
		if dag, err := CurrentDagqlServer(ctx); err == nil {
			if selectable, err := dag.ToSelectable(ctx, res); err == nil {
				return selectable, nil
			}
		}
		return res, nil
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
	case dagql.ObjectResult[*ModuleObject]:
		parentCall, err := x.ResultCall()
		if err != nil {
			return nil, fmt.Errorf("module object SDK input call frame: %w", err)
		}
		return t.objectToSDKInput(ctx, parentCall, x.Self())
	case *ModuleObject:
		return t.objectToSDKInput(ctx, dagql.CurrentCall(ctx), x)
	case dagql.IDable:
		dag, err := CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("current dagql server: %w", err)
		}
		id, err := x.ID()
		if err != nil {
			return nil, fmt.Errorf("load object ID: %w", err)
		}
		if id == nil || id.EngineResultID() == 0 {
			return nil, fmt.Errorf("load object ID: expected attached result ID")
		}
		val, err := dag.Load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load ID: %w", err)
		}
		switch x := val.(type) {
		case dagql.ObjectResult[*ModuleObject]:
			parentCall, err := x.ResultCall()
			if err != nil {
				return nil, fmt.Errorf("loaded module object SDK input call frame: %w", err)
			}
			return t.objectToSDKInput(ctx, parentCall, x.Self())
		default:
			return nil, fmt.Errorf("unexpected value type %T", x)
		}
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle %T", t, x)
	}
}

func (t *ModuleObjectType) objectToSDKInput(ctx context.Context, parentCall *dagql.ResultCall, obj *ModuleObject) (map[string]any, error) {
	fields, err := obj.collectionSDKFields(ctx)
	if err != nil {
		return nil, err
	}
	return moduleObjectFieldsToSDKInput(ctx, t, parentCall, fields)
}

func moduleObjectFieldsToSDKInput(ctx context.Context, t *ModuleObjectType, parentCall *dagql.ResultCall, fields map[string]any) (map[string]any, error) {
	if len(fields) == 0 {
		return map[string]any{}, nil
	}
	converted := make(map[string]any, len(fields))
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		value := fields[name]
		fieldTypeDef, ok := t.typeDef.FieldByOriginalName(name)
		if !ok {
			updated, err := unknownModuleObjectValueToSDKInput(value)
			if err != nil {
				return nil, fmt.Errorf("convert private field %q: %w", name, err)
			}
			converted[name] = updated
			continue
		}
		modType, ok, err := NewUserMod(t.mod).ModTypeFor(ctx, fieldTypeDef.TypeDef.Self(), true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for field %q: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("could not find mod type for field %q", name)
		}
		fieldCtx := ctx
		if fieldCall := dagql.ChildFieldCall(parentCall, fieldTypeDef.Name, fieldTypeDef.TypeDef.Self().ToType()); fieldCall != nil {
			fieldCtx = dagql.ContextWithCall(ctx, fieldCall)
		}
		updated, err := moduleObjectValueToSDKInput(fieldCtx, modType, value)
		if err != nil {
			return nil, fmt.Errorf("convert field %q: %w", name, err)
		}
		converted[name] = updated
	}
	return converted, nil
}

func moduleObjectValueToSDKInput(ctx context.Context, modType ModType, value any) (any, error) {
	typeDef, err := modType.TypeDef(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve mod type typedef: %w", err)
	}
	switch typeDef.Self().Kind {
	case TypeDefKindObject, TypeDefKindInterface:
		switch value := value.(type) {
		case nil:
			return nil, nil
		case dagql.AnyResult:
			id, err := value.ID()
			if err != nil {
				return nil, err
			}
			if id != nil {
				if id.EngineResultID() == 0 {
					return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
				}
				return id.Encode()
			}
			return modType.ConvertToSDKInput(ctx, value.Unwrap())
		case dagql.IDable:
			id, err := value.ID()
			if err != nil {
				return nil, err
			}
			if id == nil {
				return nil, nil
			}
			if id.EngineResultID() == 0 {
				return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
			}
			return id.Encode()
		case *call.ID:
			if value == nil {
				return nil, nil
			}
			if value.EngineResultID() == 0 {
				return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
			}
			return value.Encode()
		case call.ID:
			if value.EngineResultID() == 0 {
				return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
			}
			return value.Encode()
		default:
			typed, err := modType.ConvertFromSDKResult(ctx, value)
			if err != nil {
				return nil, err
			}
			return modType.ConvertToSDKInput(ctx, typed)
		}
	}
	switch modType := modType.(type) {
	case *PrimitiveType:
		return value, nil
	case *ListType:
		if value == nil {
			return nil, nil
		}
		switch value := value.(type) {
		case []any:
			items := make([]any, 0, len(value))
			for i, item := range value {
				updated, err := moduleObjectValueToSDKInput(ctx, modType.Underlying, item)
				if err != nil {
					return nil, fmt.Errorf("item %d: %w", i, err)
				}
				items = append(items, updated)
			}
			return items, nil
		default:
			return value, nil
		}
	case *NullableType:
		if value == nil {
			return nil, nil
		}
		return moduleObjectValueToSDKInput(ctx, modType.Inner, value)
	default:
		typed, err := modType.ConvertFromSDKResult(ctx, value)
		if err != nil {
			return nil, err
		}
		return modType.ConvertToSDKInput(ctx, typed)
	}
}

func unknownModuleObjectValueToSDKInput(value any) (any, error) {
	switch value := value.(type) {
	case nil:
		return nil, nil
	case dagql.AnyResult:
		id, err := value.ID()
		if err != nil {
			return nil, err
		}
		if id != nil {
			if id.EngineResultID() == 0 {
				return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
			}
			return id.Encode()
		}
		return unknownModuleObjectValueToSDKInput(value.Unwrap())
	case dagql.IDable:
		id, err := value.ID()
		if err != nil {
			return nil, err
		}
		if id == nil {
			return nil, nil
		}
		if id.EngineResultID() == 0 {
			return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
		}
		return id.Encode()
	case *call.ID:
		if value == nil {
			return nil, nil
		}
		if value.EngineResultID() == 0 {
			return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
		}
		return value.Encode()
	case call.ID:
		if value.EngineResultID() == 0 {
			return nil, fmt.Errorf("module object SDK input requires engine-result IDs")
		}
		return value.Encode()
	case []any:
		items := make([]any, 0, len(value))
		for i, item := range value {
			updated, err := unknownModuleObjectValueToSDKInput(item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, updated)
		}
		return items, nil
	case map[string]any:
		fields := make(map[string]any, len(value))
		for _, name := range slices.Sorted(maps.Keys(value)) {
			updated, err := unknownModuleObjectValueToSDKInput(value[name])
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			fields[name] = updated
		}
		return fields, nil
	default:
		return value, nil
	}
}

func (t *ModuleObjectType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}

	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok {
		return fmt.Errorf("expected *ModuleObject, got %T", value)
	}
	objFields := obj.Fields
	if obj.TypeDef.Collection != nil && obj.TypeDef.Collection.Enabled {
		base := obj
		if obj.CollectionBase != nil {
			base = obj.CollectionBase.Unwrap().(*ModuleObject)
		}
		keys, err := base.collectionKeys(ctx)
		if err != nil {
			return err
		}
		texts := make([]string, 0, len(keys))
		for _, key := range keys {
			texts = append(texts, key.text)
		}
		// Compare base contents, not session-local handles. Never recurse through
		// the original collection's reference to itself.
		if err := content.CollectKeyed(collectionBaseField, func() error { return content.CollectJSONable(texts) }); err != nil {
			return err
		}
	}
	parentCall, err := value.ResultCall()
	if err != nil {
		return fmt.Errorf("resolve module object result call: %w", err)
	}

	// Iterate fields in sorted order to produce a deterministic hash.
	for _, k := range slices.Sorted(maps.Keys(objFields)) {
		v := objFields[k]
		fieldTypeDef, ok := t.typeDef.FieldByOriginalName(k)
		if !ok {
			// this is a private field; do best-effort collection, because we don't
			// have type hints for these, but the user may still store IDs in them
			if err := content.CollectKeyed(k, func() error {
				return content.CollectUnknown(ctx, v)
			}); err != nil {
				return err
			}
			continue
		}

		modType, ok, err := NewUserMod(t.mod).ModTypeFor(ctx, fieldTypeDef.TypeDef.Self(), true)
		if err != nil {
			return fmt.Errorf("failed to get mod type for field %q: %w", k, err)
		}
		if !ok {
			return fmt.Errorf("could not find mod type for field %q", k)
		}

		fieldCtx := ctx
		if fieldCall := dagql.ChildFieldCall(parentCall, fieldTypeDef.Name, fieldTypeDef.TypeDef.Self().ToType()); fieldCall != nil {
			fieldCtx = dagql.ContextWithCall(ctx, fieldCall)
		}
		typed, err := modType.ConvertFromSDKResult(fieldCtx, v)
		if err != nil {
			return fmt.Errorf("failed to convert field %q: %w", k, err)
		}
		if err := content.CollectKeyed(k, func() error {
			return modType.CollectContent(fieldCtx, typed, content)
		}); err != nil {
			return fmt.Errorf("failed to collect content for field %q: %w", k, err)
		}
	}

	return nil
}

func (t *ModuleObjectType) TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error) {
	var sourceMap dagql.Optional[dagql.ID[*SourceMap]]
	var err error
	if t.typeDef.SourceMap.Valid {
		sourceMap, err = OptionalResultIDInput(t.typeDef.SourceMap.Value)
		if err != nil {
			return dagql.ObjectResult[*TypeDef]{}, err
		}
	}
	return SelectReferenceTypeDef(ctx, "withObject", "name", t.typeDef.Name,
		dagql.NamedInput{Name: "description", Value: dagql.String(t.typeDef.Description)},
		dagql.NamedInput{Name: "sourceMap", Value: sourceMap},
		dagql.NamedInput{Name: "deprecated", Value: OptString(t.typeDef.Deprecated)},
		dagql.NamedInput{Name: "sourceModuleName", Value: OptSourceModuleName(t.typeDef.SourceModuleName)},
	)
}

type Callable interface {
	Call(context.Context, *CallOpts) (dagql.AnyResult, error)
	ReturnType() (ModType, error)
	ArgType(argName string) (ModType, error)
	DynamicInputsForCall(context.Context, dagql.AnyResult, map[string]dagql.Input, call.View, *dagql.CallRequest) error
}

func (t *ModuleObjectType) GetCallable(ctx context.Context, name string) (Callable, error) {
	mod := NewUserMod(t.mod)

	if field, ok := t.typeDef.FieldByName(name); ok {
		fieldType, ok, err := mod.ModTypeFor(ctx, field.TypeDef.Self(), true)
		if err != nil {
			return nil, fmt.Errorf("get field return type: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("could not find type for field type: %s", field.TypeDef.Self().ToType())
		}
		return &CallableField{
			Module: t.mod.Self(),
			Field:  field,
			Return: fieldType,
		}, nil
	}

	if fun, ok := t.typeDef.FunctionByName(name); ok {
		if t.mod.Self() == nil {
			return nil, fmt.Errorf("module object type %q is missing module result wrapper", t.typeDef.Name)
		}
		return NewModFunction(
			ctx,
			t.mod,
			t.typeDef,
			fun,
		)
	}
	return nil, fmt.Errorf("no field or function %q found on object %q", name, t.typeDef.Name)
}

type ModuleObject struct {
	Module dagql.ObjectResult[*Module]

	TypeDef *ObjectTypeDef
	Fields  map[string]any

	CollectionBatch bool
	// Set once when the value is attached. Ordinary copies retain this reference.
	CollectionBase dagql.AnyResult
}

var _ dagql.HasDependencyResults = (*ModuleObject)(nil)
var _ dagql.HasResultReference = (*ModuleObject)(nil)

const (
	persistedModuleObjectValueKindNull      = "null"
	persistedModuleObjectValueKindResultRef = "result_id"
	persistedModuleObjectValueKindCallID    = "call_id"
	persistedModuleObjectValueKindScalar    = "scalar_json"
	persistedModuleObjectValueKindArray     = "array"
	persistedModuleObjectValueKindObject    = "object"
)

type persistedModuleObjectValue struct {
	Kind       string                                `json:"kind"`
	ResultID   uint64                                `json:"resultID,omitempty"`
	CallID     string                                `json:"callID,omitempty"`
	ScalarJSON json.RawMessage                       `json:"scalarJSON,omitempty"`
	Items      []persistedModuleObjectValue          `json:"items,omitempty"`
	Fields     map[string]persistedModuleObjectValue `json:"fields,omitempty"`
}

type persistedModuleObjectPayload struct {
	Fields         map[string]persistedModuleObjectValue `json:"fields,omitempty"`
	CollectionBase uint64                                `json:"collectionBase,omitempty"`
}

func (obj *ModuleObject) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if obj == nil {
		return nil, nil
	}
	owned, err := obj.attachCollectionBase(self, attach)
	if err != nil {
		return nil, err
	}

	owned := make([]dagql.AnyResult, 0, 1+len(obj.Fields))
	if obj.Module.Self() != nil {
		// The object's class resolves its fields against this module, and its
		// provider scopes it for every call. The object must own it: the
		// module was loaded by some session, and the object outlives that
		// session in the cache. This holds for empty objects too, and it is
		// acyclic because a module owns only its source, typedefs, runtime,
		// and dependency modules.
		attached, err := attach(obj.Module)
		if err != nil {
			return nil, fmt.Errorf("attach module object module: %w", err)
		}
		module, ok := attached.(dagql.ObjectResult[*Module])
		if !ok {
			return nil, fmt.Errorf("attach module object module: unexpected result %T", attached)
		}
		obj.Module = module
		owned = append(owned, module)
	}
	if len(obj.Fields) == 0 {
		return owned, nil
	}

	if obj.Module.Self() == nil || obj.TypeDef == nil {
		for _, name := range slices.Sorted(maps.Keys(obj.Fields)) {
			updated, deps, err := attachModuleObjectValue(ctx, attach, obj.Fields[name])
			if err != nil {
				return nil, fmt.Errorf("attach module object field %q: %w", name, err)
			}
			obj.Fields[name] = updated
			owned = append(owned, deps...)
		}
		return owned, nil
	}

	var parentCall *dagql.ResultCall
	if self != nil {
		call, err := self.ResultCall()
		if err != nil {
			return nil, fmt.Errorf("module object attach owned results: resolve parent call: %w", err)
		}
		parentCall = call
	}

	modInst := NewUserMod(obj.Module)
	for _, name := range slices.Sorted(maps.Keys(obj.Fields)) {
		fieldTypeDef, ok := obj.TypeDef.FieldByOriginalName(name)
		if !ok {
			updated, deps, err := attachModuleObjectValue(ctx, attach, obj.Fields[name])
			if err != nil {
				return nil, fmt.Errorf("attach module object field %q: %w", name, err)
			}
			obj.Fields[name] = updated
			owned = append(owned, deps...)
			continue
		}

		modType, ok, err := modInst.ModTypeFor(ctx, fieldTypeDef.TypeDef.Self(), true)
		if err != nil {
			return nil, fmt.Errorf("attach module object field %q: resolve mod type: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("attach module object field %q: missing mod type", name)
		}

		fieldCtx := ctx
		if fieldCall := dagql.ChildFieldCall(parentCall, fieldTypeDef.Name, fieldTypeDef.TypeDef.Self().ToType()); fieldCall != nil {
			fieldCtx = dagql.ContextWithCall(ctx, fieldCall)
		}

		updated, deps, err := attachTypedModuleObjectValue(fieldCtx, modType, obj.Fields[name], attach)
		if err != nil {
			return nil, fmt.Errorf("attach module object field %q: %w", name, err)
		}
		obj.Fields[name] = updated
		owned = append(owned, deps...)
	}
	return owned, nil
}

func attachTypedModuleObjectValue(
	ctx context.Context,
	modType ModType,
	val any,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) (any, []dagql.AnyResult, error) {
	switch x := modType.(type) {
	case *NullableType:
		if val == nil {
			return nil, nil, nil
		}
		return attachTypedModuleObjectValue(ctx, x.Inner, val, attach)
	case *ListType:
		if val == nil {
			return nil, nil, nil
		}
		items, ok := val.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("expected []any, got %T", val)
		}
		updatedItems := make([]any, 0, len(items))
		owned := make([]dagql.AnyResult, 0)
		for i, item := range items {
			itemCtx := ctx
			if curCall := dagql.CurrentCall(ctx); curCall != nil {
				itemCall := cloneResultCall(curCall)
				itemCall.Nth = int64(i + 1)
				if itemCall.Type != nil {
					itemCall.Type = itemCall.Type.Elem
				}
				itemCtx = dagql.ContextWithCall(ctx, itemCall)
			}
			updated, deps, err := attachTypedModuleObjectValue(itemCtx, x.Underlying, item, attach)
			if err != nil {
				return nil, nil, fmt.Errorf("item %d: %w", i, err)
			}
			updatedItems = append(updatedItems, updated)
			owned = append(owned, deps...)
		}
		return updatedItems, owned, nil
	}

	typeDef, err := modType.TypeDef(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve mod type typedef: %w", err)
	}
	if typeDef.Self() == nil {
		return val, nil, nil
	}
	switch typeDef.Self().Kind {
	case TypeDefKindObject, TypeDefKindInterface:
		typed, err := modType.ConvertFromSDKResult(ctx, val)
		if err != nil {
			return nil, nil, err
		}
		if typed == nil {
			return nil, nil, nil
		}
		attached, err := attach(typed)
		if err != nil {
			return nil, nil, err
		}
		// Handles must retain the attached reference so persistence can relocate
		// them. Inline object maps keep their SDK representation while the
		// attached result still supplies the dependency edge.
		switch val.(type) {
		case dagql.AnyResult, dagql.IDable, string:
			return attached, []dagql.AnyResult{attached}, nil
		default:
			return val, []dagql.AnyResult{attached}, nil
		}
	default:
		return val, nil, nil
	}
}

func attachModuleObjectValue(
	ctx context.Context,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
	val any,
) (any, []dagql.AnyResult, error) {
	switch x := val.(type) {
	case nil:
		return nil, nil, nil
	case dagql.AnyResult:
		attached, err := attach(x)
		if err != nil {
			return nil, nil, err
		}
		return attached, []dagql.AnyResult{attached}, nil
	case string:
		var id call.ID
		if err := id.Decode(x); err == nil {
			return attachModuleObjectHandleValue(ctx, attach, val, &id)
		} else {
			// Not an encoded ID: an ordinary scalar string.
			return val, nil, nil
		}
	case dagql.IDable:
		id, err := x.ID()
		if err != nil {
			return nil, nil, err
		}
		return attachModuleObjectHandleValue(ctx, attach, val, id)
	case *call.ID:
		return attachModuleObjectHandleValue(ctx, attach, val, x)
	case call.ID:
		return attachModuleObjectHandleValue(ctx, attach, val, &x)
	case []any:
		items := make([]any, 0, len(x))
		owned := make([]dagql.AnyResult, 0)
		for i, item := range x {
			updated, deps, err := attachModuleObjectValue(ctx, attach, item)
			if err != nil {
				return nil, nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, updated)
			owned = append(owned, deps...)
		}
		return items, owned, nil
	case map[string]any:
		fields := make(map[string]any, len(x))
		owned := make([]dagql.AnyResult, 0)
		for _, name := range slices.Sorted(maps.Keys(x)) {
			updated, deps, err := attachModuleObjectValue(ctx, attach, x[name])
			if err != nil {
				return nil, nil, fmt.Errorf("field %q: %w", name, err)
			}
			fields[name] = updated
			owned = append(owned, deps...)
		}
		return fields, owned, nil
	default:
		return val, nil, nil
	}
}

// attachModuleObjectHandleValue attaches a module object field value that
// stores a handle-form ID (as an encoded string, a *call.ID, or an IDable)
// by loading its result so attachment can record a real dependency edge and
// later serialization can mint a live handle. Private (undeclared) fields
// are the main source of these values: SDKs serialize stored objects as ID
// strings, and without a dependency edge the referenced result can be
// released while the owning object's cached state is still reusable,
// leaving later sessions to load a dangling handle that fails with
// "missing shared result". Non-ID strings are ordinary scalars, and
// recipe-form IDs are self-contained and loadable, so both are left
// as-is.
func attachModuleObjectHandleValue(
	ctx context.Context,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
	orig any,
	id *call.ID,
) (any, []dagql.AnyResult, error) {
	if id == nil || !id.IsHandle() {
		return orig, nil, nil
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		if srv := dagql.AttachmentResolverServer(ctx); srv != nil {
			dag = srv
		} else {
			// The value names a live engine result; silently keeping the raw
			// handle would recreate the dangling-reference bug, so fail loudly.
			return nil, nil, fmt.Errorf("attach stored handle %q: no dagql server: %w", id.Display(), err)
		}
	}
	res, err := dag.LoadType(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("load stored handle %q: %w", id.Display(), err)
	}
	attached, err := attach(res)
	if err != nil {
		return nil, nil, err
	}
	return attached, []dagql.AnyResult{attached}, nil
}

func persistedModuleObjectValueHasCallID(val persistedModuleObjectValue) bool {
	switch val.Kind {
	case persistedModuleObjectValueKindCallID:
		return true
	case persistedModuleObjectValueKindArray:
		for _, item := range val.Items {
			if persistedModuleObjectValueHasCallID(item) {
				return true
			}
		}
	case persistedModuleObjectValueKindObject:
		for _, name := range slices.Sorted(maps.Keys(val.Fields)) {
			if persistedModuleObjectValueHasCallID(val.Fields[name]) {
				return true
			}
		}
	}
	return false
}

func (obj *ModuleObject) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if obj == nil {
		return encodePersistedObjectPayload(persistedModuleObjectPayload{})
	}
	payload := persistedModuleObjectPayload{
		Fields: make(map[string]persistedModuleObjectValue, len(obj.Fields)),
	}
	// A base that points to self is restored before the cache publishes it. Encoding
	// it as a child reference would require recursively decoding the same value.
	if obj.CollectionBase != nil && obj.CollectionBase.Unwrap() != obj {
		var err error
		payload.CollectionBase, err = encodePersistedObjectRef(enc, obj.CollectionBase, "collection base")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	fieldNames := slices.Collect(maps.Keys(obj.Fields))
	slices.Sort(fieldNames)
	for _, name := range fieldNames {
		encoded, err := encodePersistedModuleObjectValue(enc, obj.Fields[name])
		if err != nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted module object field %q: %w", name, err)
		}
		if _, ok := obj.TypeDef.FieldByOriginalName(name); ok && persistedModuleObjectValueHasCallID(encoded) {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted module object field %q: unexpected raw call ID in semantic field", name)
		}
		payload.Fields[name] = encoded
	}
	return encodePersistedObjectPayload(payload)
}

func (obj *ModuleObject) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, jsonBytes json.RawMessage) (dagql.Typed, error) {
	if obj == nil || obj.Module.Self() == nil || obj.TypeDef == nil {
		return nil, fmt.Errorf("decode persisted module object: missing module/type definition")
	}
	var payload persistedModuleObjectPayload
	if len(jsonBytes) > 0 {
		if err := json.Unmarshal(jsonBytes, &payload); err != nil {
			return nil, fmt.Errorf("decode persisted module object fields: %w", err)
		}
	}
	fields := make(map[string]any, len(payload.Fields))
	for name, encoded := range payload.Fields {
		if _, ok := obj.TypeDef.FieldByOriginalName(name); ok && persistedModuleObjectValueHasCallID(encoded) {
			return nil, fmt.Errorf("decode persisted module object field %q: unexpected raw call ID in semantic field", name)
		}
		decoded, err := decodePersistedModuleObjectValue(ctx, dec, encoded)
		if err != nil {
			return nil, fmt.Errorf("decode persisted module object field %q: %w", name, err)
		}
		fields[name] = decoded
	}
	decoded := &ModuleObject{
		Module:          obj.Module,
		TypeDef:         obj.TypeDef,
		Fields:          fields,
		CollectionBatch: obj.CollectionBatch,
	}
	if payload.CollectionBase != 0 {
		var err error
		decoded.CollectionBase, err = loadPersistedResultByResultID(ctx, dec, payload.CollectionBase, "collection base")
		if err != nil {
			return nil, err
		}
	}
	return decoded, nil
}

// DecodedDependencyResults reports the module a decoded object captured from
// its decoding class. The persisted payload carries only the fields; the
// module comes from whichever schema decoded the row, and that schema's
// module is owned only by the decoding session. The decoded row must own it
// so its class stays usable after that session ends.
func (obj *ModuleObject) DecodedDependencyResults() []dagql.AnyResult {
	if obj == nil || obj.Module.Self() == nil {
		return nil
	}
	return []dagql.AnyResult{obj.Module}
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func encodePersistedModuleObjectValue(enc *dagql.PersistEncodeContext, val any) (persistedModuleObjectValue, error) {
	if val == nil {
		return persistedModuleObjectValue{Kind: persistedModuleObjectValueKindNull}, nil
	}

	switch x := val.(type) {
	case dagql.AnyResult:
		resultID, err := encodePersistedObjectRef(enc, x, "module object value")
		if err != nil {
			return persistedModuleObjectValue{}, err
		}
		return persistedModuleObjectValue{
			Kind:     persistedModuleObjectValueKindResultRef,
			ResultID: resultID,
		}, nil
	case dagql.IDable:
		id, err := x.ID()
		if err != nil {
			return persistedModuleObjectValue{}, err
		}
		if id == nil {
			return persistedModuleObjectValue{Kind: persistedModuleObjectValueKindNull}, nil
		}
		encodedID, err := encodePersistedCallID(enc, id)
		if err != nil {
			return persistedModuleObjectValue{}, err
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindCallID,
			CallID: encodedID,
		}, nil
	case *call.ID:
		if x == nil {
			return persistedModuleObjectValue{Kind: persistedModuleObjectValueKindNull}, nil
		}
		encodedID, err := encodePersistedCallID(enc, x)
		if err != nil {
			return persistedModuleObjectValue{}, err
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindCallID,
			CallID: encodedID,
		}, nil
	case call.ID:
		encodedID, err := encodePersistedCallID(enc, &x)
		if err != nil {
			return persistedModuleObjectValue{}, err
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindCallID,
			CallID: encodedID,
		}, nil
	case json.RawMessage:
		return persistedModuleObjectScalarValue(x)
	case []byte:
		return persistedModuleObjectScalarValue(x)
	case map[string]any:
		fields := make(map[string]persistedModuleObjectValue, len(x))
		fieldNames := slices.Collect(maps.Keys(x))
		slices.Sort(fieldNames)
		for _, name := range fieldNames {
			encoded, err := encodePersistedModuleObjectValue(enc, x[name])
			if err != nil {
				return persistedModuleObjectValue{}, fmt.Errorf("field %q: %w", name, err)
			}
			fields[name] = encoded
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindObject,
			Fields: fields,
		}, nil
	case []any:
		items := make([]persistedModuleObjectValue, 0, len(x))
		for i, item := range x {
			encoded, err := encodePersistedModuleObjectValue(enc, item)
			if err != nil {
				return persistedModuleObjectValue{}, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, encoded)
		}
		return persistedModuleObjectValue{
			Kind:  persistedModuleObjectValueKindArray,
			Items: items,
		}, nil
	}

	rv := reflect.ValueOf(val)
	if !rv.IsValid() {
		return persistedModuleObjectValue{Kind: persistedModuleObjectValueKindNull}, nil
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return persistedModuleObjectValue{Kind: persistedModuleObjectValueKindNull}, nil
		}
		return encodePersistedModuleObjectValue(enc, rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return persistedModuleObjectScalarValue(val)
		}
		items := make([]persistedModuleObjectValue, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			encoded, err := encodePersistedModuleObjectValue(enc, rv.Index(i).Interface())
			if err != nil {
				return persistedModuleObjectValue{}, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, encoded)
		}
		return persistedModuleObjectValue{
			Kind:  persistedModuleObjectValueKindArray,
			Items: items,
		}, nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return persistedModuleObjectValue{}, fmt.Errorf("unsupported map key type %s", rv.Type().Key())
		}
		fields := make(map[string]persistedModuleObjectValue, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			name := iter.Key().String()
			encoded, err := encodePersistedModuleObjectValue(enc, iter.Value().Interface())
			if err != nil {
				return persistedModuleObjectValue{}, fmt.Errorf("field %q: %w", name, err)
			}
			fields[name] = encoded
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindObject,
			Fields: fields,
		}, nil
	case reflect.Struct:
		fields := make(map[string]persistedModuleObjectValue)
		rt := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			field := rt.Field(i)
			name, ok := persistedModuleObjectFieldName(field)
			if !ok {
				continue
			}
			encoded, err := encodePersistedModuleObjectValue(enc, rv.Field(i).Interface())
			if err != nil {
				return persistedModuleObjectValue{}, fmt.Errorf("field %q: %w", name, err)
			}
			fields[name] = encoded
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindObject,
			Fields: fields,
		}, nil
	default:
		return persistedModuleObjectScalarValue(val)
	}
}

func persistedModuleObjectScalarValue(val any) (persistedModuleObjectValue, error) {
	raw, err := json.Marshal(val)
	if err != nil {
		return persistedModuleObjectValue{}, err
	}
	return persistedModuleObjectValue{
		Kind:       persistedModuleObjectValueKindScalar,
		ScalarJSON: raw,
	}, nil
}

func decodePersistedModuleObjectValue(ctx context.Context, dec *dagql.PersistDecodeContext, val persistedModuleObjectValue) (any, error) {
	switch val.Kind {
	case "", persistedModuleObjectValueKindNull:
		return nil, nil
	case persistedModuleObjectValueKindResultRef:
		return loadPersistedResultByResultID(ctx, dec, val.ResultID, "module object value")
	case persistedModuleObjectValueKindCallID:
		return decodePersistedCallID(dec, val.CallID)
	case persistedModuleObjectValueKindScalar:
		if len(val.ScalarJSON) == 0 {
			return nil, nil
		}
		// Untyped numbers stay exact: the SDK conversion path already decodes
		// returned JSON with UseNumber, so a restored field must not round
		// through float64.
		decoded, err := dagql.DecodeLosslessJSON(val.ScalarJSON)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	case persistedModuleObjectValueKindArray:
		items := make([]any, 0, len(val.Items))
		for i, item := range val.Items {
			decoded, err := decodePersistedModuleObjectValue(ctx, dec, item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, decoded)
		}
		return items, nil
	case persistedModuleObjectValueKindObject:
		fields := make(map[string]any, len(val.Fields))
		fieldNames := slices.Collect(maps.Keys(val.Fields))
		slices.Sort(fieldNames)
		for _, name := range fieldNames {
			decoded, err := decodePersistedModuleObjectValue(ctx, dec, val.Fields[name])
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			fields[name] = decoded
		}
		return fields, nil
	default:
		return nil, fmt.Errorf("unsupported kind %q", val.Kind)
	}
}

func persistedModuleObjectFieldName(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" {
		return "", false
	}
	if tag, ok := field.Tag.Lookup("json"); ok {
		name := strings.Split(tag, ",")[0]
		switch name {
		case "-":
			return "", false
		case "":
			return field.Name, true
		default:
			return name, true
		}
	}
	return field.Name, true
}

func (obj *ModuleObject) Type() *ast.Type {
	name := obj.TypeDef.Name
	if obj.CollectionBatch {
		name += "_Batch"
	}
	return &ast.Type{
		NamedType: name,
		NonNull:   true,
	}
}

func (obj *ModuleObject) TypeDescription() string {
	return formatGqlDescription(obj.TypeDef.Description)
}

func (obj *ModuleObject) TypeDefinition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Object,
		Name: obj.Type().Name(),
	}
	if obj.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, obj.TypeDef.SourceMap.Value.Self().TypeDirective())
	}
	if obj.TypeDef.Collection != nil && obj.TypeDef.Collection.Enabled && !obj.CollectionBatch {
		def.Directives = append(def.Directives, &ast.Directive{Name: "collection"})
	}
	return def
}

func (obj *ModuleObject) Install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error {
	if obj.Module.Self() == nil {
		return fmt.Errorf("installing object %q too early", obj.TypeDef.Name)
	}

	var opt InstallOpts
	if len(opts) > 0 {
		opt = opts[0]
	}

	classOpts := dagql.ClassOpts[*ModuleObject]{
		Typed: obj,
	}

	installDirectives := []*ast.Directive{}
	if obj.TypeDef.SourceMap.Valid {
		classOpts.SourceMap = obj.TypeDef.SourceMap.Value.Self().TypeDirective()
		installDirectives = append(installDirectives, obj.TypeDef.SourceMap.Value.Self().TypeDirective())
	}

	class := dagql.NewClass(dag, classOpts)
	if obj.isMainObject() && !opt.SkipConstructor && !opt.Entrypoint {
		if err := obj.installConstructor(ctx, dag); err != nil {
			return fmt.Errorf("failed to install constructor: %w", err)
		}
	}
	var fields []dagql.Field[*ModuleObject]
	var err error
	if obj.TypeDef.Collection != nil && obj.TypeDef.Collection.Enabled {
		fields, err = obj.collectionFields(ctx, dag)
	} else {
		fields, err = obj.fields()
		if err != nil {
			return err
		}
		var funs []dagql.Field[*ModuleObject]
		funs, err = obj.functions(ctx, dag)
		fields = append(fields, funs...)
	}
	if err != nil {
		return err
	}

	// Engine-only state transfer is deliberately absent from TypeDef.Functions:
	// it must not become an author method, tool, or entrypoint proxy.
	rebind, err := obj.stateRebindField(dag)
	if err != nil {
		return fmt.Errorf("install state rebind: %w", err)
	}
	fields = append(fields, rebind)

	class.Install(fields...)
	dag.InstallObject(class, installDirectives...)

	if obj.isMainObject() && opt.Entrypoint {
		if err := obj.installEntrypointMethods(ctx, dag, fields); err != nil {
			return fmt.Errorf("failed to install entrypoint methods: %w", err)
		}
	}

	return nil
}

func (obj *ModuleObject) isMainObject() bool {
	if obj.CollectionBatch {
		return false
	}
	if src := obj.Module.Self().GetSource(); src != nil && src.Entrypoint != nil {
		return obj.TypeDef.Constructor.Valid
	}
	return gqlObjectName(obj.TypeDef.OriginalName) == gqlObjectName(obj.Module.Self().OriginalName)
}

func (obj *ModuleObject) installConstructor(ctx context.Context, dag *dagql.Server) error {
	objDef := obj.TypeDef
	mod := obj.Module.Self()
	moduleID, moduleProvider, err := NewUserMod(obj.Module).FieldModule()
	if err != nil {
		return fmt.Errorf("failed to resolve module identity for object %q constructor: %w", objDef.Name, err)
	}

	// if no constructor defined, install a basic one that initializes an empty object
	if !objDef.Constructor.Valid {
		// Prefer the object's description; fall back to the module's
		// description so that dependency constructors on Query always
		// carry the module's doc string when the struct itself has none.
		desc := formatGqlDescription(objDef.Description)
		if desc == "" {
			desc = formatGqlDescription(mod.Description)
		}
		spec := dagql.FieldSpec{
			Name:             gqlFieldName(mod.Name()),
			Description:      desc,
			Type:             obj,
			Module:           moduleID,
			ModuleProvider:   moduleProvider,
			DeprecatedReason: objDef.Deprecated,
			IsPersistable:    true,
		}

		if objDef.SourceMap.Valid {
			spec.Directives = append(spec.Directives, objDef.SourceMap.Value.Self().TypeDirective())
		}

		dag.Root().ObjectType().Extend(
			spec,
			func(ctx context.Context, self dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentCall(ctx, &ModuleObject{
					Module:  obj.Module,
					TypeDef: objDef,
					Fields:  map[string]any{},
				})
			},
		)
		return nil
	}

	// use explicit user-defined constructor if provided
	fnTypeDef := objDef.Constructor.Value.Self()
	if fnTypeDef.ReturnType.Self().Kind != TypeDefKindObject {
		return fmt.Errorf("constructor function for object %s must return that object", objDef.OriginalName)
	}
	if fnTypeDef.ReturnType.Self().AsObject.Value.Self().OriginalName != objDef.OriginalName {
		return fmt.Errorf("constructor function for object %s must return that object", objDef.OriginalName)
	}
	if obj.Module.Self() == nil {
		return fmt.Errorf("install constructor for object %q without module result wrapper", objDef.Name)
	}

	fn, err := NewModFunction(ctx, obj.Module, objDef, fnTypeDef)
	if err != nil {
		return fmt.Errorf("failed to create function: %w", err)
	}
	if err := fn.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return fmt.Errorf("failed to merge user defaults: %w", err)
	}
	spec, err := fn.metadata.FieldSpec(ctx, NewUserMod(obj.Module))
	if err != nil {
		return fmt.Errorf("failed to get field spec for constructor: %w", err)
	}
	spec.Name = gqlFieldName(mod.Name())
	if spec.Description == "" {
		spec.Description = formatGqlDescription(objDef.Description)
	}
	if spec.Description == "" {
		spec.Description = formatGqlDescription(mod.Description)
	}
	spec.Module = moduleID
	spec.ModuleProvider = moduleProvider
	spec.GetDynamicInput = fn.DynamicInputsForCall
	spec.ImplicitInputs = append(spec.ImplicitInputs, fn.cacheImplicitInputs()...)

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
				Server:       dag,
			})
		},
	)

	return nil
}

func (obj *ModuleObject) installEntrypointMethods(ctx context.Context, dag *dagql.Server, fields []dagql.Field[*ModuleObject]) error {
	moduleID, moduleProvider, err := NewUserMod(obj.Module).FieldModule()
	if err != nil {
		return fmt.Errorf("failed to resolve module identity for entrypoint object %q: %w", obj.TypeDef.Name, err)
	}
	constructorName := gqlFieldName(obj.Module.Self().Name())

	// Build constructor arg specs from the module's type definition
	// rather than looking them up from the server — the constructor
	// is not installed on the outer server when Entrypoint is set.
	var constructorArgs []dagql.InputSpec
	if obj.TypeDef.Constructor.Valid {
		fn, err := NewModFunction(ctx, obj.Module, obj.TypeDef, obj.TypeDef.Constructor.Value.Self())
		if err != nil {
			return fmt.Errorf("failed to create constructor function: %w", err)
		}
		if err := fn.mergeUserDefaultsTypeDefs(ctx); err != nil {
			return fmt.Errorf("failed to merge constructor user defaults: %w", err)
		}
		spec, err := fn.metadata.FieldSpec(ctx, NewUserMod(obj.Module))
		if err != nil {
			return fmt.Errorf("failed to get constructor field spec: %w", err)
		}
		constructorArgs = spec.Args.Inputs(dag.View)
	}

	// Install `with` field on Query that stores constructor args for
	// entrypoint proxy resolvers to forward to the constructor.
	// Only installed when the constructor has arguments.
	if len(constructorArgs) > 0 {
		// Use the original constructor's description if available,
		// since `with` IS the user-facing constructor.
		withDesc := obj.TypeDef.Constructor.Value.Self().Description
		if withDesc == "" {
			withDesc = fmt.Sprintf("Configure the %s constructor arguments.", obj.Module.Self().Name())
		}
		withSpec := dagql.FieldSpec{
			Name:           "with",
			Description:    withDesc,
			Type:           &Query{},
			Module:         moduleID,
			ModuleProvider: moduleProvider,
			Args:           dagql.NewInputSpecs(constructorArgs...),
			DoNotCache:     "Pure routing; the inner module constructor has its own caching policy.",
			NoTelemetry:    true,
		}
		dag.Root().ObjectType().Extend(
			withSpec,
			func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
				query, ok := dagql.UnwrapAs[*Query](self)
				if !ok {
					return nil, fmt.Errorf("expected *Query, got %T", self)
				}
				// store only the args the caller actually provided — found in
				// the call frame, built from the query AST — so the
				// constructor still applies its own defaults, including .env
				// user defaults, to the rest.
				var explicit map[string]bool
				if frame := dagql.CurrentCall(ctx); frame != nil {
					explicit = make(map[string]bool, len(frame.Args))
					for _, arg := range frame.Args {
						explicit[arg.Name] = true
					}
				}
				cp := query.Clone()
				cp.ConstructorArgs = make(map[string]dagql.Input, len(args))
				for k, v := range args {
					if explicit != nil && !explicit[k] {
						continue
					}
					cp.ConstructorArgs[k] = v
				}
				return dagql.NewObjectResultForCurrentCall(ctx, dag, cp)
			},
		)
	}

	// Forward the installed public fields, including collection operations.
	for _, field := range fields {
		if strings.HasPrefix(field.Spec.Name, "__") {
			continue
		}
		proxySpec := *field.Spec
		proxySpec.GetDynamicInput = nil
		proxySpec.ImplicitInputs = nil
		proxySpec.Trivial = false
		proxySpec.Directives = slices.DeleteFunc(slices.Clone(proxySpec.Directives), func(d *ast.Directive) bool {
			return d.Name == trivialFieldDirectiveName
		})
		// Proxy specs only carry the method's own args — constructor args
		// are stored on the Query via the `with` field.
		proxySpec.Module = moduleID
		proxySpec.ModuleProvider = moduleProvider
		proxySpec.DoNotCache = "Entrypoint proxy is pure routing; the inner constructor and method calls cache on their own."
		proxySpec.NoTelemetry = true

		methodName := proxySpec.Name
		methodArgs := proxySpec.Args.Inputs(dag.View)
		proxy := func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			// Prevent dag.Select from marking the inner constructor
			// and method calls as internal — they are the real
			// user-facing calls and should appear in telemetry.
			ctx = dagql.WithNonInternalTelemetry(ctx)
			// Desugar through the canonical server where the real
			// constructor lives (not shadowed by proxy fields).
			canonical := dag.Canonical()
			// Read constructor args from the Query (set by `with`).
			query, _ := dagql.UnwrapAs[*Query](self)
			var ctorNamedArgs []dagql.NamedInput
			if query != nil && query.ConstructorArgs != nil {
				ctorNamedArgs = orderedNamedInputs(constructorArgs, query.ConstructorArgs)
			}
			ctorNamedArgs = WithBoundWorkspaceArgs(ctx, canonical, constructorArgs, ctorNamedArgs)
			var result dagql.AnyResult
			if err := canonical.Select(ctx, canonical.Root(), &result,
				dagql.Selector{
					Field: constructorName,
					Args:  ctorNamedArgs,
				},
				dagql.Selector{
					Field: methodName,
					Args:  orderedNamedInputs(methodArgs, args),
				},
			); err != nil {
				return nil, err
			}
			return result, nil
		}
		dag.Root().ObjectType().Extend(proxySpec, proxy)
	}

	return nil
}

func orderedNamedInputs(specs []dagql.InputSpec, args map[string]dagql.Input) []dagql.NamedInput {
	if len(args) == 0 {
		return nil
	}

	inputs := make([]dagql.NamedInput, 0, len(specs))
	for _, spec := range specs {
		arg, ok := args[spec.Name]
		if !ok {
			continue
		}
		inputs = append(inputs, dagql.NamedInput{
			Name:  spec.Name,
			Value: arg,
		})
	}
	return inputs
}

func (obj *ModuleObject) fields() (fields []dagql.Field[*ModuleObject], err error) {
	for _, field := range obj.TypeDef.Fields {
		objField, err := objField(obj.Module, field.Self())
		if err != nil {
			return nil, err
		}
		fields = append(fields, objField)
	}
	return fields, nil
}

func (obj *ModuleObject) functions(ctx context.Context, dag *dagql.Server) ([]dagql.Field[*ModuleObject], error) {
	var fields []dagql.Field[*ModuleObject]
	for _, fn := range obj.TypeDef.Functions {
		fun := fn.Self()
		authored := fun
		if fun.CheckReturnType.Self() != nil {
			authored = fun.Clone()
			authored.ReturnType = fun.CheckReturnType
		}
		field, err := objFun(ctx, obj.Module, obj.TypeDef, authored, dag)
		if err != nil {
			return nil, err
		}
		if fun.CheckReturnType.Self() == nil {
			fields = append(fields, field)
			continue
		}
		legacySpec := *field.Spec
		legacySpec.ViewFilter = BeforeVersion("v1.0.0-0")
		legacy := field
		legacy.Spec = &legacySpec
		fields = append(fields, legacy)

		projectionSpec := *field.Spec
		projectionSpec.Type = fun.ReturnType.Self().ToTyped()
		projectionSpec.ViewFilter = AfterVersion("v1.0.0-0")
		projectionSpec.IsPersistable = false
		projectionSpec.TTL = 0
		fields = append(fields, dagql.Field[*ModuleObject]{
			Spec: &projectionSpec,
			Func: func(ctx context.Context, receiver dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				workspace, _ := WorkspaceFromContext(ctx)
				var inputs []CallInput
				for name, value := range args {
					inputs = append(inputs, CallInput{Name: name, Value: value})
				}
				sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
				return dagql.NewObjectResultForCurrentCall(ctx, dag, &Check{
					Assertion: dagql.NonNull(dagql.String(fun.Description)),
					Receiver:  receiver, Function: fun.Name, Inputs: inputs, Workspace: workspace, CacheTTL: field.Spec.TTL,
				})
			},
		})
	}
	return fields, nil
}

func objField(mod dagql.ObjectResult[*Module], field *FieldTypeDef) (dagql.Field[*ModuleObject], error) {
	moduleID, moduleProvider, err := NewUserMod(mod).FieldModule()
	if err != nil {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("failed to resolve module identity for field %q: %w", field.Name, err)
	}
	spec := &dagql.FieldSpec{
		Name:             field.Name,
		Description:      field.Description,
		Type:             field.TypeDef.Self().ToTyped(),
		Module:           moduleID,
		ModuleProvider:   moduleProvider,
		DeprecatedReason: field.Deprecated,
		Trivial:          true,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{
		Name: trivialFieldDirectiveName,
	})
	if field.SourceMap.Valid {
		spec.Directives = append(spec.Directives, field.SourceMap.Value.Self().TypeDirective())
	}
	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			modType, ok, err := NewUserMod(mod).ModTypeFor(ctx, field.TypeDef.Self(), true)
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
	}, nil
}

// objFun creates a dagql.Field for a function defined on a module object type.
// This is used during the GraphQL schema installation process to convert
// user-defined functions in module object types into callable GraphQL fields.
//
// Flow:
// 1. Called from ModuleObject.functions() during ModuleObject.Install()
// 2. Creates a ModFunction wrapper around the user's function definition
// 3. Generates a GraphQL field spec from the function signature
// 4. Returns a dagql.Field that can handle GraphQL calls by:
//   - Converting GraphQL arguments to CallInput format
//   - Calling the underlying ModFunction with the parent object context
//   - Returning the function result as a dagql.AnyResult
//
// The resulting field enables users to call their custom functions as GraphQL
// fields on their object types, with proper argument handling and caching.
func objFun(ctx context.Context, mod dagql.ObjectResult[*Module], objDef *ObjectTypeDef, fun *Function, dag *dagql.Server) (dagql.Field[*ModuleObject], error) {
	var f dagql.Field[*ModuleObject]
	if mod.Self() == nil {
		return f, fmt.Errorf("install function %q without module result wrapper", fun.Name)
	}
	modFun, err := NewModFunction(
		ctx,
		mod,
		objDef,
		fun,
	)
	if err != nil {
		return f, fmt.Errorf("failed to create function %q: %w", fun.Name, err)
	}
	// Apply local user defaults to the function's arguments, so that they show
	// up in installed typedefs (for introspection)
	if err := modFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return f, fmt.Errorf("failed to merge user defaults for %q: %w", fun.Name, err)
	}
	spec, err := modFun.metadata.FieldSpec(ctx, NewUserMod(mod))
	if err != nil {
		return f, fmt.Errorf("failed to get field spec: %w", err)
	}
	moduleID, moduleProvider, err := NewUserMod(mod).FieldModule()
	if err != nil {
		return f, fmt.Errorf("failed to resolve module identity for function %q: %w", fun.Name, err)
	}
	spec.Module = moduleID
	spec.ModuleProvider = moduleProvider
	spec.GetDynamicInput = modFun.DynamicInputsForCall
	spec.ImplicitInputs = append(spec.ImplicitInputs, modFun.cacheImplicitInputs()...)

	return dagql.Field[*ModuleObject]{
		Spec: &spec,
		Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			opts := &CallOpts{
				ParentTyped:    obj,
				ParentFields:   obj.Self().Fields,
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

func (f *CallableField) DynamicInputsForCall(
	ctx context.Context,
	parent dagql.AnyResult,
	args map[string]dagql.Input,
	view call.View,
	req *dagql.CallRequest,
) error {
	return nil
}
