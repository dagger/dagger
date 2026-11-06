package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

const rebindModuleObjectStateField = "__rebindState"

// RebindModuleObjectState selects an engine-owned, replayable operation on the
// target revision's initial object. The previous receiver is an ID argument, not
// a closure capture or a replacement result under the old object's identity.
// State that tool calls recorded wins, even when null or empty (see
// toolStateFields); every other field takes initial's value, so a reload
// re-resolves the object's construction: settings, defaults, and session-bound
// values such as secrets. The result retains the target module and class.
func RebindModuleObjectState(ctx context.Context, srv *dagql.Server, initial, previous dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	target, old, err := moduleObjectsForRebind(initial, previous)
	if err != nil {
		return nil, err
	}
	same, err := sameModuleImplementation(ctx, target.Module, old.Module)
	if err != nil {
		return nil, err
	}
	if same {
		// Keep the previous identity when rebinding would not change anything:
		// a reload with no code or configuration change is a no-op.
		unchanged, err := rebindKeepsPrevious(ctx, target, old, previous)
		if err != nil {
			return nil, err
		}
		if unchanged {
			return previous, nil
		}
	}
	previousID, err := previous.ID()
	if err != nil {
		return nil, fmt.Errorf("previous receiver ID: %w", err)
	}
	// Select dispatches through the server's class registry, not just the
	// receiver wrapper. Resolve the target recipe's defining server so a caller
	// still serving the old revision cannot run its rebind field instead.
	targetID, err := initial.RecipeID(ctx)
	if err != nil {
		return nil, fmt.Errorf("target receiver recipe: %w", err)
	}
	targetClass, targetServer, found, err := srv.ObjectTypeAndServerForID(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("target receiver class is unavailable")
	}
	initial, err = targetClass.New(initial)
	if err != nil {
		return nil, fmt.Errorf("bind target receiver class: %w", err)
	}
	var result dagql.AnyObjectResult
	if err := targetServer.Select(ctx, initial, &result, dagql.Selector{
		Field: rebindModuleObjectStateField,
		Args:  []dagql.NamedInput{{Name: "previous", Value: dagql.NewAnyID(previousID)}},
	}); err != nil {
		return nil, fmt.Errorf("rebind %s state: %w", target.TypeDef.OriginalName, err)
	}
	return result, nil
}

// Equivalent implementation-scoped modules may still have distinct cache result
// handles. Compare their canonical content identities, including module variants.
func sameModuleImplementation(ctx context.Context, left, right dagql.ObjectResult[*Module]) (bool, error) {
	leftScoped, err := ImplementationScopedModule(ctx, left)
	if err != nil {
		return false, err
	}
	rightScoped, err := ImplementationScopedModule(ctx, right)
	if err != nil {
		return false, err
	}
	leftDigest, err := leftScoped.ContentPreferredDigest(ctx)
	if err != nil {
		return false, err
	}
	rightDigest, err := rightScoped.ContentPreferredDigest(ctx)
	if err != nil {
		return false, err
	}
	return leftDigest == rightDigest, nil
}

func moduleObjectsForRebind(initial, previous dagql.AnyObjectResult) (*ModuleObject, *ModuleObject, error) {
	if initial == nil || previous == nil {
		return nil, nil, fmt.Errorf("state rebind requires both target and previous receivers")
	}
	target, ok := dagql.UnwrapAs[*ModuleObject](initial)
	if !ok || target == nil || target.Module.Self() == nil || target.TypeDef == nil {
		return nil, nil, fmt.Errorf("target receiver must be a module object")
	}
	old, ok := dagql.UnwrapAs[*ModuleObject](previous)
	if !ok || old == nil || old.Module.Self() == nil || old.TypeDef == nil {
		return nil, nil, fmt.Errorf("previous receiver must be a module object")
	}
	if target.TypeDef.OriginalName != old.TypeDef.OriginalName || target.Module.Self().OriginalName != old.Module.Self().OriginalName {
		return nil, nil, fmt.Errorf("cannot rebind %s.%s to %s.%s", old.Module.Self().OriginalName, old.TypeDef.OriginalName, target.Module.Self().OriginalName, target.TypeDef.OriginalName)
	}
	return target, old, nil
}

func (obj *ModuleObject) stateRebindField(srv *dagql.Server) (dagql.Field[*ModuleObject], error) {
	module, moduleProvider, err := NewUserMod(obj.Module).FieldModule()
	if err != nil {
		return dagql.Field[*ModuleObject]{}, err
	}
	installed := newInstalledServer(srv)
	return dagql.Field[*ModuleObject]{
		Spec: &dagql.FieldSpec{
			Name:           rebindModuleObjectStateField,
			Type:           obj,
			Module:         module,
			ModuleProvider: moduleProvider,
			IsPersistable:  true,
			Args:           dagql.NewInputSpecs(dagql.InputSpec{Name: "previous", Type: dagql.AnyID{}}),
		},
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
			id, ok := args["previous"].(dagql.AnyID)
			if !ok {
				return nil, fmt.Errorf("invalid previous receiver ID")
			}
			previousID, err := id.ID()
			if err != nil {
				return nil, err
			}
			srv, err := installed.forObject(ctx, self)
			if err != nil {
				return nil, err
			}
			previous, err := srv.Load(ctx, previousID)
			if err != nil {
				return nil, fmt.Errorf("load previous receiver: %w", err)
			}
			target, old, err := moduleObjectsForRebind(self, previous)
			if err != nil {
				return nil, err
			}
			// Recipe provenance installs an implementation-scoped module, while
			// the receiver may retain its original workspace-bound module. Compare
			// the canonical implementation identities rather than raw handles.
			sameClass, err := sameModuleImplementation(ctx, target.Module, obj.Module)
			if err != nil {
				return nil, err
			}
			if !sameClass {
				return nil, fmt.Errorf("target receiver class belongs to a different module revision")
			}
			state, err := toolStateFields(ctx, previous)
			if err != nil {
				return nil, err
			}
			fields, err := rebindModuleObjectFields(ctx, target, old, state)
			if err != nil {
				return nil, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &ModuleObject{
				Module: target.Module, TypeDef: target.TypeDef, Fields: fields,
			})
		},
	}, nil
}

// toolState names the fields of a module object that hold state recorded
// by tool calls, as opposed to values its construction produced.
type toolState struct {
	// all treats every field as state. It is set when the recipe does not say
	// which fields tool calls set: a same-type chain recorded as the
	// producing calls themselves, from before state was recorded field-wise.
	all    bool
	fields map[string]struct{}
}

// carried returns the subset of previous that is recorded state.
func (s toolState) carried(previous map[string]any) map[string]any {
	if s.all {
		return previous
	}
	out := make(map[string]any, len(s.fields))
	for name := range s.fields {
		if value, ok := previous[name]; ok {
			out[name] = value
		}
	}
	return out
}

// toolStateFields reads which fields of obj tool calls set, from its
// recipe. A same-type tool return is recorded as __withField frames on the
// previous state (see WithModuleObjectFields), and a reload as a
// __rebindState frame whose previous argument carries the earlier state's
// recipe, so the recipe names every field state was ever written to. Whatever
// sits below those frames is a construction, and its fields are configuration:
// constructor arguments (often settings), defaults, and values resolved
// against the session that built it, such as a Secret read from the host.
//
// Only state is worth carrying across a reload. Configuration is re-resolved
// by the reload itself; carrying it over would pin the old revision's
// defaults, ignore edited settings, and keep session-bound values whose
// client may be gone.
func toolStateFields(ctx context.Context, obj dagql.AnyObjectResult) (toolState, error) {
	state := toolState{fields: map[string]struct{}{}}
	id, err := obj.RecipeID(ctx)
	if err != nil {
		return state, fmt.Errorf("state recipe: %w", err)
	}
	collectToolState(id, &state)
	return state, nil
}

func collectToolState(id *call.ID, state *toolState) {
	for id != nil && !state.all {
		switch id.Field() {
		case withModuleObjectFieldName:
			name, ok := literalArg(id, "name").(string)
			if !ok {
				state.all = true
				return
			}
			state.fields[name] = struct{}{}
		case rebindModuleObjectStateField:
			previous, ok := literalArg(id, "previous").(*call.ID)
			if !ok {
				state.all = true
				return
			}
			collectToolState(previous, state)
		default:
			// A call on an object of the same type that returns that type is a
			// state transition recorded as the call itself; its effect on the
			// fields is unknown, so keep them all.
			if recv := id.Receiver(); recv != nil && recv.Type() != nil && id.Type() != nil &&
				recv.Type().NamedType() == id.Type().NamedType() {
				state.all = true
			}
			return
		}
		id = id.Receiver()
	}
}

func literalArg(id *call.ID, name string) any {
	arg := id.Arg(name)
	if arg == nil || arg.Value() == nil {
		return nil
	}
	return arg.Value().ToInput()
}

// rebindKeepsPrevious reports whether rebinding previous onto target would
// leave its fields as they are.
func rebindKeepsPrevious(ctx context.Context, target, old *ModuleObject, previous dagql.AnyObjectResult) (bool, error) {
	state, err := toolStateFields(ctx, previous)
	if err != nil {
		return false, err
	}
	if state.all {
		return true, nil
	}
	fields, _, err := overlayModuleObjectState(target.Fields, state.carried(old.Fields), target.TypeDef, old.TypeDef)
	if err != nil {
		return false, fmt.Errorf("%s: %w", target.TypeDef.OriginalName, err)
	}
	changed, err := changedStateFields(old.Fields, fields)
	if err != nil {
		return false, err
	}
	return len(changed) == 0, nil
}

// rebindModuleObjectFields overlays the previous revision's recorded state onto
// the target's constructed defaults (see overlayModuleObjectState).
func rebindModuleObjectFields(ctx context.Context, target, old *ModuleObject, state toolState) (map[string]any, error) {
	typeName := target.TypeDef.OriginalName
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)
	fields, warnings, err := overlayModuleObjectState(target.Fields, state.carried(old.Fields), target.TypeDef, old.TypeDef)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", typeName, err)
	}
	for _, warning := range warnings {
		logger.Warn("carried tool state across revisions with a caveat",
			"object", typeName,
			"field", warning.Field,
			"reason", warning.Reason)
	}
	return fields, nil
}

// stateOverlayWarning is a non-fatal observation about a carried-over field.
type stateOverlayWarning struct {
	Field  string
	Reason string
}

// overlayModuleObjectState returns previous overlaid onto initial: existing
// values win, including nulls and empty collections; keys only initial has
// take the new revision's defaults.
//
// The engine cannot see private fields in the GraphQL typedef, but it holds
// three things at rebind time: the previous receiver's serialized state (every
// key, private included), the new revision's constructor output (its defaults,
// private included, since SDKs serialize whole objects), and both revisions'
// public typedefs. Those support exactly the checks made here: a public field
// whose type changed between typedefs fails, as does a value whose JSON kind
// differs from the new revision's default for the same key. A public field the
// new revision no longer has at all is dropped, since the old typedef proves
// it was removed on purpose. Keys neither typedef nor the constructor knows are
// passed through with a warning: they are private fields the new constructor
// left unset, or renamed/removed private fields, and only the SDK's own
// parent-state decoder can tell — it runs on the next tool call and remains
// the authority on what its objects accept.
func overlayModuleObjectState(initial, previous map[string]any, target, source *ObjectTypeDef) (map[string]any, []stateOverlayWarning, error) {
	targetTypes := publicFieldTypes(target)
	sourceTypes := publicFieldTypes(source)

	var warnings []stateOverlayWarning
	fields := maps.Clone(initial)
	if fields == nil {
		fields = make(map[string]any)
	}
	for _, name := range slices.Sorted(maps.Keys(previous)) {
		value := previous[name]
		oldType, wasPublic := sourceTypes[name]
		newType, isPublic := targetTypes[name]
		initialValue, hasDefault := initial[name]

		switch {
		case wasPublic && isPublic && oldType != newType:
			return nil, nil, fmt.Errorf("field %q changed type (%s -> %s); change the withTools version to reset its state", name, oldType, newType)
		case wasPublic && !isPublic && !hasDefault:
			warnings = append(warnings, stateOverlayWarning{name, "dropped: the field no longer exists in the new revision"})
			continue
		case !wasPublic && !isPublic && !hasDefault:
			warnings = append(warnings, stateOverlayWarning{name, "not emitted by the new revision's constructor; its SDK decoder decides what to do with it"})
		}

		if hasDefault {
			if err := checkStateKind(name, value, initialValue); err != nil {
				return nil, nil, err
			}
		}
		fields[name] = value
	}
	return fields, warnings, nil
}

// publicFieldTypes maps each public field's original (SDK-side) name to its
// GraphQL type signature, e.g. "[String!]!". Nullability and list element
// types are part of the signature.
func publicFieldTypes(def *ObjectTypeDef) map[string]string {
	types := make(map[string]string)
	if def == nil {
		return types
	}
	for _, field := range def.Fields {
		f := field.Self()
		if f == nil || f.TypeDef.Self() == nil {
			continue
		}
		types[f.OriginalName] = f.TypeDef.Self().ToType().String()
	}
	return types
}

// stateKind is the JSON-level shape of a serialized field value. It is the
// most the engine can know about a private field.
type stateKind string

const (
	stateKindNull      stateKind = "null"
	stateKindBoolean   stateKind = "boolean"
	stateKindNumber    stateKind = "number"
	stateKindString    stateKind = "string"
	stateKindList      stateKind = "list"
	stateKindObject    stateKind = "object"
	stateKindReference stateKind = "reference"
	stateKindUnknown   stateKind = "unknown"
)

func kindOfState(value any) stateKind {
	switch value.(type) {
	case nil:
		return stateKindNull
	case bool:
		return stateKindBoolean
	case string:
		return stateKindString
	case json.Number, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return stateKindNumber
	case []any:
		return stateKindList
	case map[string]any:
		return stateKindObject
	case dagql.AnyResult, dagql.IDable, dagql.Typed, *call.ID, call.ID:
		// Engine-side objects and handles; the SDK sees them as ID strings.
		return stateKindReference
	default:
		return stateKindUnknown
	}
}

// compatibleStateKinds reports whether a carried-over value of kind previous
// can stand in for a default of kind initial. Nulls carry no shape and are
// always compatible; references travel as ID strings.
func compatibleStateKinds(previous, initial stateKind) bool {
	if previous == initial || previous == stateKindNull || initial == stateKindNull || previous == stateKindUnknown || initial == stateKindUnknown {
		return true
	}
	isStringy := func(k stateKind) bool { return k == stateKindString || k == stateKindReference }
	return isStringy(previous) && isStringy(initial)
}

// checkStateKind compares the shapes of a previous value and the new default
// for the same key, recursing into nested objects (common keys only) and the
// first element of non-empty lists.
func checkStateKind(path string, previous, initial any) error {
	oldKind, newKind := kindOfState(previous), kindOfState(initial)
	if !compatibleStateKinds(oldKind, newKind) {
		return fmt.Errorf("field %q: previous value is a %s but the new revision's default is a %s; change the withTools version to reset its state", path, oldKind, newKind)
	}
	switch oldKind {
	case stateKindObject:
		oldObj, ok1 := previous.(map[string]any)
		newObj, ok2 := initial.(map[string]any)
		if !ok1 || !ok2 {
			return nil
		}
		for _, k := range slices.Sorted(maps.Keys(oldObj)) {
			if _, shared := newObj[k]; !shared {
				continue
			}
			if err := checkStateKind(path+"."+k, oldObj[k], newObj[k]); err != nil {
				return err
			}
		}
	case stateKindList:
		oldList, ok1 := previous.([]any)
		newList, ok2 := initial.([]any)
		if ok1 && ok2 && len(oldList) > 0 && len(newList) > 0 {
			return checkStateKind(path+"[]", oldList[0], newList[0])
		}
	}
	return nil
}
