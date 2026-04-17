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

// indicates an ast field is deprecated
const deprecatedDirectiveName = "deprecated"

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
	case map[string]any:
		return dagql.NewResultForCurrentCall(ctx, &ModuleObject{
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
	case dagql.ObjectResult[*ModuleObject]:
		parentCall, err := x.ResultCall()
		if err != nil {
			return nil, fmt.Errorf("module object SDK input call frame: %w", err)
		}
		return moduleObjectFieldsToSDKInput(ctx, t, parentCall, x.Self().Fields)
	case dagql.ObjectResult[*InterfaceAnnotatedValue]:
		parentCall, err := x.ResultCall()
		if err != nil {
			return nil, fmt.Errorf("interface SDK input call frame: %w", err)
		}
		return moduleObjectFieldsToSDKInput(ctx, t, parentCall, x.Self().Fields)
	case *ModuleObject:
		return moduleObjectFieldsToSDKInput(ctx, t, dagql.CurrentCall(ctx), x.Fields)
	case *InterfaceAnnotatedValue:
		return moduleObjectFieldsToSDKInput(ctx, t, dagql.CurrentCall(ctx), x.Fields)
	case DynamicID:
		dag, err := CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("current dagql server: %w", err)
		}
		id, err := x.ID()
		if err != nil {
			return nil, fmt.Errorf("load DynamicID ID: %w", err)
		}
		if id == nil || id.EngineResultID() == 0 {
			return nil, fmt.Errorf("load DynamicID: expected attached result ID")
		}
		val, err := dag.Load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load DynamicID: %w", err)
		}
		switch x := val.(type) {
		case dagql.ObjectResult[*ModuleObject]:
			parentCall, err := x.ResultCall()
			if err != nil {
				return nil, fmt.Errorf("loaded module object SDK input call frame: %w", err)
			}
			return moduleObjectFieldsToSDKInput(ctx, t, parentCall, x.Self().Fields)
		case dagql.ObjectResult[*InterfaceAnnotatedValue]:
			parentCall, err := x.ResultCall()
			if err != nil {
				return nil, fmt.Errorf("loaded interface SDK input call frame: %w", err)
			}
			return moduleObjectFieldsToSDKInput(ctx, t, parentCall, x.Self().Fields)
		default:
			return nil, fmt.Errorf("unexpected value type %T", x)
		}
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle %T", t, x)
	}
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
			updated, err := unknownModuleObjectValueToSDKInput(ctx, value)
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

func unknownModuleObjectValueToSDKInput(ctx context.Context, value any) (any, error) {
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
		return unknownModuleObjectValueToSDKInput(ctx, value.Unwrap())
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
			updated, err := unknownModuleObjectValueToSDKInput(ctx, item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, updated)
		}
		return items, nil
	case map[string]any:
		fields := make(map[string]any, len(value))
		for _, name := range slices.Sorted(maps.Keys(value)) {
			updated, err := unknownModuleObjectValueToSDKInput(ctx, value[name])
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
	var objFields map[string]any
	if obj, ok := dagql.UnwrapAs[*ModuleObject](value); ok {
		objFields = obj.Fields
	} else if iface, ok := dagql.UnwrapAs[*InterfaceAnnotatedValue](value); ok {
		objFields = iface.Fields
	} else {
		return fmt.Errorf("expected *ModuleObject, got %T", value)
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
	return SelectTypeDef(ctx, dagql.Selector{
		Field: "withObject",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(t.typeDef.Name)},
			{Name: "description", Value: dagql.String(t.typeDef.Description)},
			{Name: "sourceMap", Value: sourceMap},
			{Name: "deprecated", Value: OptString(t.typeDef.Deprecated)},
			{Name: "sourceModuleName", Value: OptSourceModuleName(t.typeDef.SourceModuleName)},
		},
	})
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
}

var _ dagql.HasDependencyResults = (*ModuleObject)(nil)

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
	Fields map[string]persistedModuleObjectValue `json:"fields,omitempty"`
}

func (obj *ModuleObject) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if obj == nil || len(obj.Fields) == 0 {
		return nil, nil
	}

	if obj.Module.Self() == nil || obj.TypeDef == nil {
		owned := make([]dagql.AnyResult, 0)
		for _, name := range slices.Sorted(maps.Keys(obj.Fields)) {
			updated, deps, err := attachModuleObjectValue(attach, obj.Fields[name])
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
	owned := make([]dagql.AnyResult, 0)
	for _, name := range slices.Sorted(maps.Keys(obj.Fields)) {
		fieldTypeDef, ok := obj.TypeDef.FieldByOriginalName(name)
		if !ok {
			updated, deps, err := attachModuleObjectValue(attach, obj.Fields[name])
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
		if _, ok := val.(dagql.AnyResult); ok {
			return attached, []dagql.AnyResult{attached}, nil
		}
		if _, ok := val.(dagql.IDable); ok {
			return attached, []dagql.AnyResult{attached}, nil
		}
		return val, []dagql.AnyResult{attached}, nil
	default:
		return val, nil, nil
	}
}

func attachModuleObjectValue(
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
	case []any:
		items := make([]any, 0, len(x))
		owned := make([]dagql.AnyResult, 0)
		for i, item := range x {
			updated, deps, err := attachModuleObjectValue(attach, item)
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
			updated, deps, err := attachModuleObjectValue(attach, x[name])
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

func (obj *ModuleObject) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if obj == nil || len(obj.Fields) == 0 {
		return json.Marshal(persistedModuleObjectPayload{})
	}
	payload := persistedModuleObjectPayload{
		Fields: make(map[string]persistedModuleObjectValue, len(obj.Fields)),
	}
	fieldNames := slices.Collect(maps.Keys(obj.Fields))
	slices.Sort(fieldNames)
	for _, name := range fieldNames {
		encoded, err := encodePersistedModuleObjectValue(ctx, cache, obj.Fields[name])
		if err != nil {
			return nil, fmt.Errorf("encode persisted module object field %q: %w", name, err)
		}
		if _, ok := obj.TypeDef.FieldByOriginalName(name); ok && persistedModuleObjectValueHasCallID(encoded) {
			return nil, fmt.Errorf("encode persisted module object field %q: unexpected raw call ID in semantic field", name)
		}
		payload.Fields[name] = encoded
	}
	return json.Marshal(payload)
}

func (obj *ModuleObject) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	jsonBytes json.RawMessage,
) (dagql.Typed, error) {
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
		decoded, err := decodePersistedModuleObjectValue(ctx, dag, encoded)
		if err != nil {
			return nil, fmt.Errorf("decode persisted module object field %q: %w", name, err)
		}
		fields[name] = decoded
	}
	return &ModuleObject{
		Module:  obj.Module,
		TypeDef: obj.TypeDef,
		Fields:  fields,
	}, nil
}

func encodePersistedModuleObjectValue(ctx context.Context, cache dagql.PersistedObjectCache, val any) (persistedModuleObjectValue, error) {
	if val == nil {
		return persistedModuleObjectValue{Kind: persistedModuleObjectValueKindNull}, nil
	}

	switch x := val.(type) {
	case dagql.AnyResult:
		resultID, err := encodePersistedObjectRef(cache, x, "module object value")
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
		encodedID, err := encodePersistedCallID(id)
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
		encodedID, err := encodePersistedCallID(x)
		if err != nil {
			return persistedModuleObjectValue{}, err
		}
		return persistedModuleObjectValue{
			Kind:   persistedModuleObjectValueKindCallID,
			CallID: encodedID,
		}, nil
	case call.ID:
		encodedID, err := encodePersistedCallID(&x)
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
			encoded, err := encodePersistedModuleObjectValue(ctx, cache, x[name])
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
			encoded, err := encodePersistedModuleObjectValue(ctx, cache, item)
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
		return encodePersistedModuleObjectValue(ctx, cache, rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return persistedModuleObjectScalarValue(val)
		}
		items := make([]persistedModuleObjectValue, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			encoded, err := encodePersistedModuleObjectValue(ctx, cache, rv.Index(i).Interface())
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
			encoded, err := encodePersistedModuleObjectValue(ctx, cache, iter.Value().Interface())
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
			encoded, err := encodePersistedModuleObjectValue(ctx, cache, rv.Field(i).Interface())
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

func decodePersistedModuleObjectValue(ctx context.Context, dag *dagql.Server, val persistedModuleObjectValue) (any, error) {
	switch val.Kind {
	case "", persistedModuleObjectValueKindNull:
		return nil, nil
	case persistedModuleObjectValueKindResultRef:
		return loadPersistedResultByResultID(ctx, dag, val.ResultID, "module object value")
	case persistedModuleObjectValueKindCallID:
		return decodePersistedCallID(val.CallID)
	case persistedModuleObjectValueKindScalar:
		var decoded any
		if len(val.ScalarJSON) == 0 {
			return nil, nil
		}
		if err := json.Unmarshal(val.ScalarJSON, &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	case persistedModuleObjectValueKindArray:
		items := make([]any, 0, len(val.Items))
		for i, item := range val.Items {
			decoded, err := decodePersistedModuleObjectValue(ctx, dag, item)
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
			decoded, err := decodePersistedModuleObjectValue(ctx, dag, val.Fields[name])
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
	return &ast.Type{
		NamedType: obj.TypeDef.Name,
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
	fields, err := obj.fields(ctx)
	if err != nil {
		return err
	}

	funs, err := obj.functions(ctx, dag)
	if err != nil {
		return err
	}
	fields = append(fields, funs...)

	class.Install(fields...)
	dag.InstallObject(class, installDirectives...)

	if obj.isMainObject() && opt.Entrypoint {
		if err := obj.installEntrypointMethods(ctx, dag); err != nil {
			return fmt.Errorf("failed to install entrypoint methods: %w", err)
		}
	}

	return nil
}

func (obj *ModuleObject) isMainObject() bool {
	return gqlObjectName(obj.TypeDef.OriginalName) == gqlObjectName(obj.Module.Self().OriginalName)
}

func (obj *ModuleObject) installConstructor(ctx context.Context, dag *dagql.Server) error {
	objDef := obj.TypeDef
	mod := obj.Module.Self()
	moduleID, err := NewUserMod(obj.Module).ResultCallModule(ctx)
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

func (obj *ModuleObject) installEntrypointMethods(ctx context.Context, dag *dagql.Server) error {
	moduleID, err := NewUserMod(obj.Module).ResultCallModule(ctx)
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
			Name:        "with",
			Description: withDesc,
			Type:        &Query{},
			Module:      moduleID,
			Args:        dagql.NewInputSpecs(constructorArgs...),
			DoNotCache:  "Pure routing; the inner module constructor has its own caching policy.",
			NoTelemetry: true,
		}
		dag.Root().ObjectType().Extend(
			withSpec,
			func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
				query, ok := dagql.UnwrapAs[*Query](self)
				if !ok {
					return nil, fmt.Errorf("expected *Query, got %T", self)
				}
				cp := query.Clone()
				cp.ConstructorArgs = make(map[string]dagql.Input, len(args))
				for k, v := range args {
					cp.ConstructorArgs[k] = v
				}
				return dagql.NewObjectResultForCurrentCall(ctx, dag, cp)
			},
		)
	}

	for _, fun := range obj.TypeDef.Functions {
		modFun, err := NewModFunction(ctx, obj.Module, obj.TypeDef, fun.Self())
		if err != nil {
			return fmt.Errorf("failed to create function %q: %w", fun.Self().Name, err)
		}
		if err := modFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
			return fmt.Errorf("failed to merge user defaults for %q: %w", fun.Self().Name, err)
		}

		proxySpec, err := modFun.metadata.FieldSpec(ctx, NewUserMod(obj.Module))
		if err != nil {
			return fmt.Errorf("failed to get field spec for %q: %w", fun.Self().Name, err)
		}
		// Proxy specs only carry the method's own args — constructor args
		// are stored on the Query via the `with` field.
		proxySpec.Module = moduleID
		proxySpec.DoNotCache = "Entrypoint proxy is pure routing; the inner constructor and method calls cache on their own."
		proxySpec.NoTelemetry = true

		methodName := proxySpec.Name
		methodArgs := proxySpec.Args.Inputs(dag.View)
		dag.Root().ObjectType().Extend(
			proxySpec,
			func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
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
			},
		)
	}

	for _, field := range obj.TypeDef.Fields {
		fieldName := gqlFieldName(field.Self().Name)

		proxySpec := dagql.FieldSpec{
			Name:        fieldName,
			Description: field.Self().Description,
			Type:        field.Self().TypeDef.Self().ToTyped(),
			Module:      moduleID,
			NoTelemetry: true,
			DoNotCache:  "Entrypoint proxy is pure routing; the inner constructor and field calls cache on their own.",
		}

		proxiedFieldName := fieldName
		dag.Root().ObjectType().Extend(
			proxySpec,
			func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
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
				var result dagql.AnyResult
				if err := canonical.Select(ctx, canonical.Root(), &result,
					dagql.Selector{
						Field: constructorName,
						Args:  ctorNamedArgs,
					},
					dagql.Selector{
						Field: proxiedFieldName,
					},
				); err != nil {
					return nil, err
				}
				return result, nil
			},
		)
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

func (obj *ModuleObject) fields(ctx context.Context) (fields []dagql.Field[*ModuleObject], err error) {
	for _, field := range obj.TypeDef.Fields {
		objField, err := objField(ctx, obj.Module, field.Self())
		if err != nil {
			return nil, err
		}
		fields = append(fields, objField)
	}
	return fields, nil
}

func (obj *ModuleObject) functions(ctx context.Context, dag *dagql.Server) (fields []dagql.Field[*ModuleObject], err error) {
	objDef := obj.TypeDef
	for _, fun := range obj.TypeDef.Functions {
		objFun, err := objFun(ctx, obj.Module, objDef, fun.Self(), dag)
		if err != nil {
			return nil, err
		}
		fields = append(fields, objFun)
	}
	return
}

func objField(ctx context.Context, mod dagql.ObjectResult[*Module], field *FieldTypeDef) (dagql.Field[*ModuleObject], error) {
	moduleID, err := NewUserMod(mod).ResultCallModule(ctx)
	if err != nil {
		return dagql.Field[*ModuleObject]{}, fmt.Errorf("failed to resolve module identity for field %q: %w", field.Name, err)
	}
	spec := &dagql.FieldSpec{
		Name:             field.Name,
		Description:      field.Description,
		Type:             field.TypeDef.Self().ToTyped(),
		Module:           moduleID,
		DeprecatedReason: field.Deprecated,
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
	moduleID, err := NewUserMod(mod).ResultCallModule(ctx)
	if err != nil {
		return f, fmt.Errorf("failed to resolve module identity for function %q: %w", fun.Name, err)
	}
	spec.Module = moduleID
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
