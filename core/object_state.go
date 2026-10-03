package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// withModuleObjectFieldName is an engine-owned, hidden field on every module
// object: a pure setter for one entry of its state. A same-type tool return is
// recorded as a chain of these selected on the previous state, instead of as
// the (often @cache(Never)) call that produced it, so loading the recorded
// state never replays the producing call's side effects.
const withModuleObjectFieldName = "__withField"

// StateValue is one module object field value, mirroring
// persistedModuleObjectValue: exactly one of scalar, node, list or entries is
// set, or none for null. Object references travel as real ID arguments, so
// they are edges of the recipe (and get rewritten from handle form to recipe
// form when the recipe is persisted) rather than opaque strings.
//
// The type is deliberately not installed into any schema: it is only reached
// through engine-side selects and recipe replay, which decode arguments from
// the field spec, so it never surfaces in introspection or generated SDKs.
type StateValue struct {
	Scalar  dagql.Optional[JSON]                                            `doc:"A JSON-encoded scalar."`
	Node    dagql.Optional[dagql.AnyID]                                     `doc:"A reference to an object."`
	List    dagql.Optional[dagql.ArrayInput[dagql.InputObject[StateValue]]] `doc:"A list of values."`
	Entries dagql.Optional[dagql.ArrayInput[dagql.InputObject[StateEntry]]] `doc:"A map or inline object, by key."`
}

func (StateValue) TypeName() string { return "StateValue" }

func (StateValue) TypeDescription() string {
	return "A module object field value: exactly one of scalar, node, list or entries, or none for null."
}

// StateEntry is one key of a StateValue map.
type StateEntry struct {
	Key   string
	Value dagql.InputObject[StateValue]
}

func (StateEntry) TypeName() string { return "StateEntry" }

func (StateEntry) TypeDescription() string { return "One key of a StateValue map." }

// stateValueInputMap encodes a module object field value, in the
// representation ModuleObject.Fields holds it, as the raw (undecoded) form of
// a StateValue input. Decoding it through the StateValue decoder yields an
// input whose literal carries object references as IDs.
func stateValueInputMap(val any) (map[string]any, error) {
	switch x := val.(type) {
	case nil:
		return map[string]any{}, nil
	case dagql.AnyResult:
		id, err := x.ID()
		if err != nil {
			return nil, err
		}
		if id == nil {
			return map[string]any{}, nil
		}
		return map[string]any{"node": id}, nil
	case dagql.IDable:
		id, err := x.ID()
		if err != nil {
			return nil, err
		}
		if id == nil {
			return map[string]any{}, nil
		}
		return map[string]any{"node": id}, nil
	case *call.ID:
		if x == nil {
			return map[string]any{}, nil
		}
		return map[string]any{"node": x}, nil
	case call.ID:
		return map[string]any{"node": &x}, nil
	case []any:
		items := make([]any, 0, len(x))
		for i, item := range x {
			encoded, err := stateValueInputMap(item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, encoded)
		}
		return map[string]any{"list": items}, nil
	case map[string]any:
		entries := make([]any, 0, len(x))
		for _, key := range slices.Sorted(maps.Keys(x)) {
			encoded, err := stateValueInputMap(x[key])
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			entries = append(entries, map[string]any{"key": key, "value": encoded})
		}
		return map[string]any{"entries": entries}, nil
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encode scalar %T: %w", x, err)
		}
		return map[string]any{"scalar": string(raw)}, nil
	}
}

// stateValueInput encodes a field value as a decoded StateValue input, ready
// to pass as the value argument of __withField.
func stateValueInput(val any) (dagql.Input, error) {
	raw, err := stateValueInputMap(val)
	if err != nil {
		return nil, err
	}
	return dagql.InputObject[StateValue]{}.Decoder().DecodeInput(raw)
}

// sdkValue decodes a StateValue into the representation an SDK return puts in
// ModuleObject.Fields: object references become encoded ID strings, which
// AttachDependencyResults resolves exactly as it does for SDK-returned state.
func (v StateValue) sdkValue() (any, error) {
	set := 0
	for _, valid := range []bool{v.Scalar.Valid, v.Node.Valid, v.List.Valid, v.Entries.Valid} {
		if valid {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("state value sets more than one of scalar, node, list and entries")
	}
	switch {
	case v.Scalar.Valid:
		return dagql.DecodeLosslessJSON([]byte(v.Scalar.Value))
	case v.Node.Valid:
		id, err := v.Node.Value.ID()
		if err != nil {
			return nil, err
		}
		return id.Encode()
	case v.List.Valid:
		items := make([]any, 0, len(v.List.Value))
		for i, item := range v.List.Value {
			decoded, err := item.Value.sdkValue()
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, decoded)
		}
		return items, nil
	case v.Entries.Valid:
		fields := make(map[string]any, len(v.Entries.Value))
		for _, entry := range v.Entries.Value {
			decoded, err := entry.Value.Value.Value.sdkValue()
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", entry.Value.Key, err)
			}
			fields[entry.Value.Key] = decoded
		}
		return fields, nil
	default:
		return nil, nil
	}
}

// stateValueKey returns a canonical encoding of a field value for change
// detection: object references compare by stable ID digest, everything else
// by canonical JSON (map keys sorted).
func stateValueKey(val any) (string, error) {
	canon, err := canonicalStateValue(val)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func canonicalStateValue(val any) (any, error) {
	ref := func(id *call.ID) any {
		if id == nil {
			return nil
		}
		return map[string]any{"$ref": stableIDDigest(id).String()}
	}
	switch x := val.(type) {
	case nil:
		return nil, nil
	case dagql.AnyResult:
		id, err := x.ID()
		if err != nil {
			return nil, err
		}
		return ref(id), nil
	case dagql.IDable:
		id, err := x.ID()
		if err != nil {
			return nil, err
		}
		return ref(id), nil
	case *call.ID:
		return ref(x), nil
	case call.ID:
		return ref(&x), nil
	case []any:
		items := make([]any, 0, len(x))
		for i, item := range x {
			canon, err := canonicalStateValue(item)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			items = append(items, canon)
		}
		return items, nil
	case map[string]any:
		fields := make(map[string]any, len(x))
		for key, item := range x {
			canon, err := canonicalStateValue(item)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			fields[key] = canon
		}
		// Distinguish an inline map from a reference with a "$ref" key.
		return map[string]any{"$map": fields}, nil
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encode scalar %T: %w", x, err)
		}
		return json.RawMessage(raw), nil
	}
}

// changedStateFields returns, in sorted order, the names of fields whose
// value differs between prev and next. A field next drops counts as null:
// __withField can only set, and fields are statically typed, so a missing
// field is rare and null is the closest state to record.
func changedStateFields(prev, next map[string]any) ([]string, error) {
	names := slices.Collect(maps.Keys(next))
	for name := range prev {
		if _, ok := next[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var changed []string
	for _, name := range names {
		nextKey, err := stateValueKey(next[name])
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		prevVal, ok := prev[name]
		if ok {
			prevKey, err := stateValueKey(prevVal)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			if prevKey == nextKey {
				continue
			}
		}
		changed = append(changed, name)
	}
	return changed, nil
}

// stateSetterField installs __withField on a module object type: a pure,
// persistable setter that clones the receiver with one field replaced.
func (obj *ModuleObject) stateSetterField(srv *dagql.Server) (dagql.Field[*ModuleObject], error) {
	module, moduleProvider, err := NewUserMod(obj.Module).FieldModule()
	if err != nil {
		return dagql.Field[*ModuleObject]{}, err
	}
	installed := newInstalledServer(srv)
	return dagql.Field[*ModuleObject]{
		Spec: &dagql.FieldSpec{
			Name:           withModuleObjectFieldName,
			Type:           obj,
			Module:         module,
			ModuleProvider: moduleProvider,
			IsPersistable:  true,
			Args: dagql.NewInputSpecs(
				dagql.InputSpec{Name: "name", Type: dagql.String("")},
				dagql.InputSpec{Name: "value", Type: dagql.InputObject[StateValue]{}},
			),
		},
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
			name, ok := args["name"].(dagql.String)
			if !ok {
				return nil, fmt.Errorf("invalid field name argument %T", args["name"])
			}
			input, ok := args["value"].(dagql.InputObject[StateValue])
			if !ok {
				return nil, fmt.Errorf("invalid field value argument %T", args["value"])
			}
			value, err := input.Value.sdkValue()
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", name, err)
			}
			recv := self.Self()
			if c := recv.TypeDef.Collection; c != nil && c.Enabled {
				// See WithModuleObjectFields: a collection's base is not a field.
				return nil, fmt.Errorf("%s is a collection type: its state cannot be set field-wise", recv.TypeDef.Name)
			}
			fields := maps.Clone(recv.Fields)
			if fields == nil {
				fields = map[string]any{}
			}
			fields[string(name)] = value
			srv, err := installed.forObject(ctx, self)
			if err != nil {
				return nil, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &ModuleObject{
				Module:  recv.Module,
				TypeDef: recv.TypeDef,
				Fields:  fields,
			})
		},
	}, nil
}

// WithModuleObjectFields records next's state as a chain of __withField
// selects rooted at prev: prev!__withField(...)!__withField(...), one per
// changed field in sorted name order; a field next lacks is set to null. It
// returns a nil result when no field changed. Both must be module objects of
// the same type.
func WithModuleObjectFields(ctx context.Context, srv *dagql.Server, prev, next dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	prevObj, ok := dagql.UnwrapAs[*ModuleObject](prev)
	if !ok || prevObj == nil {
		return nil, fmt.Errorf("previous state is not a module object")
	}
	nextObj, ok := dagql.UnwrapAs[*ModuleObject](next)
	if !ok || nextObj == nil {
		return nil, fmt.Errorf("new state is not a module object")
	}
	if prevObj.TypeDef == nil || nextObj.TypeDef == nil || prevObj.TypeDef.OriginalName != nextObj.TypeDef.OriginalName {
		return nil, fmt.Errorf("previous and new state have different types")
	}
	if c := nextObj.TypeDef.Collection; c != nil && c.Enabled {
		// A collection's identity includes its CollectionBase, which lives
		// outside Fields; and a bound collection has no author methods anyway
		// (they are installed on the batch type), so this is not a case worth
		// modelling. Refuse loudly rather than record a wrong base.
		return nil, fmt.Errorf("%s is a collection type: its state cannot be recorded field-wise; bind the batch object as tools instead", nextObj.TypeDef.Name)
	}
	changed, err := changedStateFields(prevObj.Fields, nextObj.Fields)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, nil
	}
	cur := prev
	for _, name := range changed {
		value, err := stateValueInput(nextObj.Fields[name])
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		var res dagql.AnyObjectResult
		if err := srv.Select(ctx, cur, &res, dagql.Selector{
			View:  srv.View,
			Field: withModuleObjectFieldName,
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(name)},
				{Name: "value", Value: value},
			},
		}); err != nil {
			return nil, fmt.Errorf("set field %q: %w", name, err)
		}
		cur = res
	}
	return cur, nil
}
