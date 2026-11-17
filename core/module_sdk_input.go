package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

const moduleStoredFieldProjectionArg = "__daggerStoredField"
const moduleStoredFieldProjectionMarker = "v1"

type sdkInputResultsKey struct{}

// The receiver memo retains actual selected inputs beside its serialized form.
// They stay pinned to this session and are never encoded as provenance facts.
type sdkInputResults struct {
	mu     sync.Mutex
	values map[uint64]dagql.AnyResult
}

type sdkReceiverInput struct {
	JSON        []byte
	Inputs      []dagql.AnyResult
	preparation *sdkInputPreparation
}

func rememberSDKInput(ctx context.Context, value dagql.AnyResult) error {
	if value == nil || value.Unwrap() == nil {
		return nil
	}
	id, err := value.ID()
	if err != nil {
		return fmt.Errorf("selected SDK input has no attached result ID: %w", err)
	}
	if id == nil || id.EngineResultID() == 0 {
		return fmt.Errorf("selected SDK input has no attached result ID")
	}
	if inputs, ok := ctx.Value(sdkInputResultsKey{}).(*sdkInputResults); ok {
		inputs.mu.Lock()
		if inputs.values == nil {
			inputs.values = map[uint64]dagql.AnyResult{}
		}
		inputs.values[id.EngineResultID()] = value
		inputs.mu.Unlock()
	}
	importSDKInputs(ctx, []dagql.AnyResult{value})
	return nil
}

func importSDKInputs(ctx context.Context, values []dagql.AnyResult) {
	reads := currentWorkspaceInputReads(ctx)
	if reads == nil || len(values) == 0 {
		return
	}
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		reads.add(workspaceInputSummary{Issues: []string{"cannot inspect selected SDK inputs: " + err.Error()}, incomplete: true})
		return
	}
	refs := map[uint64]*dagql.ResultCallRef{}
	for _, value := range values {
		if value == nil || value.Unwrap() == nil {
			continue
		}
		id, err := value.ID()
		if err != nil || id == nil || id.EngineResultID() == 0 {
			collector.issue("selected SDK input has no attached result ID")
			continue
		}
		ref := &dagql.ResultCallRef{ResultID: id.EngineResultID()}
		collector.ref(ref)
		refs[ref.ResultID] = ref
	}
	// Selection proves only these exact outputs, not their producer ancestry.
	reads.rememberInputs(refs)
	reads.add(collector.result)
}

func storedSDKFieldToInput(ctx context.Context, owner dagql.ObjectResult[*ModuleObject], field *FieldTypeDef, typ ModType, raw any) (any, bool, error) {
	if raw == nil {
		return nil, true, nil
	}
	if id, err := owner.ID(); err != nil || id == nil || id.EngineResultID() == 0 {
		return nil, false, nil
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, false, err
	}
	ctx = dagql.ContextWithServer(ctx, srv)
	spec, ok := owner.ObjectType().FieldSpec(field.Name, srv.View)
	if ok && !spec.Trivial {
		return nil, false, nil
	}
	members, err := owner.Self().TypeDef.CollectionMembers()
	if err != nil {
		return nil, false, err
	}
	if members != nil && members.Delta != nil && members.Delta.Name == field.Name {
		// SDK conversion calculates this value; it is not the stored field.
		return nil, false, nil
	}
	// Read the exact scoped owner's fields without applying its scope to the
	// child yet. Normal completion establishes the child before view checks.
	var selected dagql.AnyResult
	if ok {
		selected, err = owner.Select(dagql.WithResultScope(ctx, nil), srv, dagql.Selector{Field: field.Name})
	} else {
		selected, err = selectModuleStoredField(ctx, srv, owner, field)
	}
	if err != nil {
		return nil, false, err
	}
	if err := rememberSDKInput(ctx, selected); err != nil {
		return nil, false, err
	}
	converted, err := selectedSDKValueToInput(ctx, typ, selected, raw)
	return converted, true, err
}

// Collections expose operations instead of their stored data fields. Read a
// hidden declared field through an internal call with the same capture and
// completion rules as an exposed field. The schema stays unchanged.
func selectModuleStoredField(ctx context.Context, srv *dagql.Server, owner dagql.ObjectResult[*ModuleObject], field *FieldTypeDef) (dagql.AnyResult, error) {
	ownerID, err := owner.ID()
	if err != nil || ownerID == nil || ownerID.EngineResultID() == 0 {
		return nil, fmt.Errorf("stored field projection requires an attached owner")
	}
	module := owner.Self().Module
	if module.Self() == nil {
		return nil, fmt.Errorf("stored field projection requires a module")
	}
	moduleID, err := module.ID()
	if err != nil || moduleID == nil || moduleID.EngineResultID() == 0 {
		return nil, fmt.Errorf("stored field projection requires an attached module")
	}
	// Stored data decoding needs the exact schema module, not a file source
	// descriptor or an implementation-scoped function provider.
	moduleCall := &dagql.ResultCallModule{Name: module.Self().Name(), ResultRef: &dagql.ResultCallRef{ResultID: moduleID.EngineResultID()}}
	frame := &dagql.ResultCall{
		Kind: dagql.ResultCallKindSynthetic, SyntheticOp: field.Name,
		Field: field.Name, Type: dagql.NewResultCallType(field.TypeDef.Self().ToType()),
		Receiver: &dagql.ResultCallRef{ResultID: ownerID.EngineResultID()}, Module: moduleCall,
		Args: []*dagql.ResultCallArg{{Name: moduleStoredFieldProjectionArg, Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: moduleStoredFieldProjectionMarker}}},
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	metadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	// objField also leaves IsPersistable false; normal preselect copies that
	// value. This internal read uses the same session memoization policy.
	request := &dagql.CallRequest{ResultCall: frame, ReceiverTypeName: owner.Type().Name(), IsPersistable: false}
	ctx = dagql.ContextWithCall(dagql.WithResultScope(ctx, nil), frame)
	ctx, resolved, complete := ObserveWorkspaceInputs(ctx, request)
	result, callErr := cache.GetOrInitCall(ctx, metadata.SessionID, srv, request, func(ctx context.Context) (dagql.AnyResult, error) {
		if resolved != nil {
			defer resolved()
		}
		return moduleObjectStoredField(ctx, module, owner, field)
	})
	if complete != nil {
		complete(result, callErr)
	}
	return result, callErr
}

func compositeSDKInputType(typ ModType) bool {
	switch typ := typ.(type) {
	case *NullableType:
		return compositeSDKInputType(typ.Inner)
	case *ListType:
		return compositeSDKInputType(typ.Underlying)
	default:
		return typ.Kind() == TypeDefKindObject || typ.Kind() == TypeDefKindInterface
	}
}

func selectedSDKValueToInput(ctx context.Context, typ ModType, selected dagql.AnyResult, raw any) (any, error) {
	if raw == nil || selected == nil || selected.Unwrap() == nil {
		return nil, nil
	}
	if optional, ok := typ.(*NullableType); ok {
		value, present := selected.DerefValue()
		if !present {
			return nil, nil
		}
		return selectedSDKValueToInput(ctx, optional.Inner, value, raw)
	}
	if list, ok := typ.(*ListType); ok {
		enumerable, ok := selected.Unwrap().(dagql.Enumerable)
		if !ok {
			return nil, fmt.Errorf("selected SDK list is not enumerable: %T", selected.Unwrap())
		}
		shapes, ok := raw.([]any)
		if !ok || len(shapes) != enumerable.Len() {
			return nil, fmt.Errorf("selected SDK list does not match its stored encoding shape")
		}
		values := make([]any, enumerable.Len())
		for i := range values {
			item, err := selected.NthValue(dagql.WithResultScope(ctx, nil), i+1)
			if err != nil {
				return nil, err
			}
			if err := rememberSDKInput(ctx, item); err != nil {
				return nil, err
			}
			values[i], err = selectedSDKValueToInput(ctx, list.Underlying, item, shapes[i])
			if err != nil {
				return nil, fmt.Errorf("SDK list item %d: %w", i, err)
			}
		}
		return values, nil
	}
	if typ.Kind() == TypeDefKindObject || typ.Kind() == TypeDefKindInterface {
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return nil, err
		}
		object, err := srv.ToSelectable(ctx, selected)
		if err != nil {
			return nil, err
		}
		if scope := dagql.CurrentResultScope(ctx); scope != nil {
			object, err = scope.Apply(ctx, srv, object)
			if err != nil {
				return nil, err
			}
		}
		if err := rememberSDKInput(ctx, object); err != nil {
			return nil, err
		}
		if _, inline := raw.(map[string]any); inline {
			return typ.ConvertToSDKInput(ctx, object)
		}
		// An already stored handle remains a handle. Its value comes from the
		// completed selection, not the raw field used to choose this shape.
		return moduleObjectValueToSDKInput(ctx, typ, object)
	}
	return nil, fmt.Errorf("unsupported selected SDK input type %T", typ)
}

func (r *sdkInputResults) snapshot() []dagql.AnyResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	var values []dagql.AnyResult
	for _, id := range slices.Sorted(maps.Keys(r.values)) {
		values = append(values, r.values[id])
	}
	return values
}
