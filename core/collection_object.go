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

// collectionKey retains the typed input used for lookup and its common text
// form used by dimensions and deltas. Text is never used to infer a key type.
type collectionKey struct {
	input    dagql.Input
	text     string
	sdkValue any
}

func collectionKeyFromInput(input dagql.Input) (collectionKey, error) {
	value := input.ToLiteral().ToInput()
	if value == nil {
		return collectionKey{}, fmt.Errorf("collection keys must not be null")
	}
	text, ok := value.(string)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return collectionKey{}, err
		}
		text = string(encoded)
	}
	return collectionKey{input: input, text: text, sdkValue: value}, nil
}

func (obj *ModuleObject) collectionKeys(ctx context.Context) ([]collectionKey, error) {
	members, err := obj.TypeDef.CollectionMembers()
	if err != nil {
		return nil, err
	}
	keyDef := members.Keys.TypeDef.Self().AsList.Value.Self().ElementTypeDef.Self()
	keyType := keyDef.ToInput()
	var enumType ModType
	if keyDef.Kind == TypeDefKindEnum {
		var ok bool
		enumType, ok, err = NewUserMod(obj.Module).ModTypeFor(ctx, keyDef, true)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("collection enum key type %s not found", keyDef.ToType())
		}
	}
	rawKeys, err := collectionSliceValues(obj.Fields[members.Keys.OriginalName])
	if err != nil {
		return nil, fmt.Errorf("collection %q keys: %w", obj.TypeDef.Name, err)
	}
	keys := make([]collectionKey, 0, len(rawKeys))
	seen := make(map[string]bool, len(rawKeys))
	for _, raw := range rawKeys {
		if raw == nil {
			return nil, fmt.Errorf("collection %q contains a null key", obj.TypeDef.Name)
		}
		input, err := keyType.Decoder().DecodeInput(raw)
		if enumType != nil {
			var converted dagql.AnyResult
			converted, err = enumType.ConvertFromSDKResult(ctx, raw)
			if err == nil {
				var ok bool
				input, ok = converted.Unwrap().(dagql.Input)
				if !ok {
					return nil, fmt.Errorf("collection enum key %T is not an input", converted.Unwrap())
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("collection %q key: %w", obj.TypeDef.Name, err)
		}
		key, err := collectionKeyFromInput(input)
		if err != nil {
			return nil, err
		}
		if seen[key.text] {
			return nil, fmt.Errorf("collection %q contains duplicate key %q", obj.TypeDef.Name, key.text)
		}
		seen[key.text] = true
		key.sdkValue = raw
		keys = append(keys, key)
	}
	return keys, nil
}

func collectionSliceValues(value any) ([]any, error) {
	if value == nil {
		return []any{}, nil
	}
	if values, ok := value.([]any); ok {
		return values, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var values []any
	err = json.Unmarshal(encoded, &values)
	return values, err
}

func (obj *ModuleObject) collectionSubset(ctx context.Context, requested []collectionKey) (*ModuleObject, error) {
	keys, err := obj.collectionKeys(ctx)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(requested))
	for _, key := range requested {
		if selected[key.text] {
			return nil, fmt.Errorf("collection %q subset contains duplicate key %q", obj.TypeDef.Name, key.text)
		}
		selected[key.text] = true
	}
	values := make([]any, 0, len(requested))
	base := make([]string, 0, len(keys))
	for _, key := range keys {
		base = append(base, key.text)
		if selected[key.text] {
			values = append(values, key.sdkValue)
			delete(selected, key.text)
		}
	}
	if len(selected) != 0 {
		return nil, fmt.Errorf("collection %q does not contain keys %q in the current subset", obj.TypeDef.Name, slices.Sorted(maps.Keys(selected)))
	}
	members, err := obj.TypeDef.CollectionMembers()
	if err != nil {
		return nil, err
	}
	subset := *obj
	subset.Fields = maps.Clone(obj.Fields)
	subset.Fields[members.Keys.OriginalName] = values
	if obj.CollectionBaseKeys == nil {
		subset.CollectionBaseKeys = base
	}
	return &subset, nil
}

func (obj *ModuleObject) collectionDelta(ctx context.Context) (*CollectionDelta, error) {
	keys, err := obj.collectionKeys(ctx)
	if err != nil {
		return nil, err
	}
	delta := &CollectionDelta{AddedKeys: []string{}, RemovedKeys: []string{}, BaseKeys: slices.Clone(obj.CollectionBaseKeys)}
	if obj.CollectionBaseKeys == nil {
		delta.BaseKeys = make([]string, 0, len(keys))
		for _, key := range keys {
			delta.BaseKeys = append(delta.BaseKeys, key.text)
		}
		return delta, nil
	}
	current := make(map[string]bool, len(keys))
	base := make(map[string]bool, len(obj.CollectionBaseKeys))
	for _, key := range obj.CollectionBaseKeys {
		base[key] = true
	}
	for _, key := range keys {
		current[key.text] = true
		if !base[key.text] {
			delta.AddedKeys = append(delta.AddedKeys, key.text)
		}
	}
	for _, key := range obj.CollectionBaseKeys {
		if !current[key] {
			delta.RemovedKeys = append(delta.RemovedKeys, key)
		}
	}
	return delta, nil
}

// collectionSDKFields fills the optional author delta without changing the
// stored value. Call this for receivers and arguments passed to module code.
func (obj *ModuleObject) collectionSDKFields(ctx context.Context) (map[string]any, error) {
	members, err := obj.TypeDef.CollectionMembers()
	if err != nil {
		return nil, err
	}
	if members == nil || members.Delta == nil {
		return obj.Fields, nil
	}
	delta, err := obj.collectionDelta(ctx)
	if err != nil {
		return nil, err
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var result dagql.ObjectResult[*CollectionDelta]
	err = dag.Select(ctx, dag.Root(), &result, dagql.Selector{
		Field: "__collectionDelta", Args: []dagql.NamedInput{
			{Name: "addedKeys", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(delta.AddedKeys...))},
			{Name: "removedKeys", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(delta.RemovedKeys...))},
			{Name: "baseKeys", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(delta.BaseKeys...))},
		},
	})
	if err != nil {
		return nil, err
	}
	fields := maps.Clone(obj.Fields)
	fields[members.Delta.OriginalName] = result
	return fields, nil
}

func (obj *ModuleObject) restoreCollectionBase(ctx context.Context) error {
	members, err := obj.TypeDef.CollectionMembers()
	if err != nil || members == nil || members.Delta == nil {
		return err
	}
	value := obj.Fields[members.Delta.OriginalName]
	if value == nil {
		return nil
	}
	var result dagql.AnyResult
	switch value := value.(type) {
	case dagql.AnyResult:
		result = value
	case string:
		var id call.ID
		if err := id.Decode(value); err != nil {
			return fmt.Errorf("collection delta ID: %w", err)
		}
		dag, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
		}
		result, err = dag.Load(ctx, &id)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid collection delta value %T", value)
	}
	delta, ok := dagql.UnwrapAs[*CollectionDelta](result)
	if !ok {
		return fmt.Errorf("invalid collection delta type %s", result.Type())
	}
	obj.CollectionBaseKeys = slices.Clone(delta.BaseKeys)
	return nil
}

func (obj *ModuleObject) collectionFields(ctx context.Context, dag *dagql.Server) ([]dagql.Field[*ModuleObject], error) {
	members, err := obj.TypeDef.CollectionMembers()
	if err != nil {
		return nil, err
	}
	if obj.CollectionBatch {
		var fields []dagql.Field[*ModuleObject]
		for _, fn := range obj.TypeDef.Functions {
			if fn.Self().Name == members.Get.Name {
				continue
			}
			field, err := objFun(ctx, obj.Module, obj.TypeDef, fn.Self(), dag)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
		}
		return fields, nil
	}
	moduleID, err := NewUserMod(obj.Module).ResultCallModule(ctx)
	if err != nil {
		return nil, err
	}
	get, err := objFun(ctx, obj.Module, obj.TypeDef, members.Get, dag)
	if err != nil {
		return nil, err
	}
	rawGet := get.Func
	get.Spec.Name = "get"
	get.Spec.Args = dagql.NewInputSpecs(dagql.InputSpec{
		Name: "key", Type: members.Get.Args[0].Self().TypeDef.Self().ToInput(),
	})
	get.Func = func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
		keys, err := self.Self().collectionKeys(ctx)
		if err != nil {
			return nil, err
		}
		key, err := collectionKeyFromInput(args["key"])
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(keys, func(k collectionKey) bool { return k.text == key.text }) {
			return nil, fmt.Errorf("collection %q does not contain key %q in the current subset", obj.TypeDef.Name, key.text)
		}
		return rawGet(ctx, self, map[string]dagql.Input{members.Get.Args[0].Self().Name: key.input}, view)
	}
	fields := []dagql.Field[*ModuleObject]{
		get,
		{
			Spec: &dagql.FieldSpec{Name: "keys", Description: "Current keys, in author order.", Type: members.Keys.TypeDef.Self().ToTyped(), Module: moduleID},
			Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				keys, err := self.Self().collectionKeys(ctx)
				if err != nil {
					return nil, err
				}
				values := make([]any, 0, len(keys))
				for _, key := range keys {
					values = append(values, key.sdkValue)
				}
				modType, ok, err := NewUserMod(obj.Module).ModTypeFor(ctx, members.Keys.TypeDef.Self(), true)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("collection keys type not found")
				}
				return modType.ConvertFromSDKResult(ctx, values)
			},
		},
		{
			Spec: &dagql.FieldSpec{Name: "list", Description: "Items in the same order as keys.", Type: dagql.DynamicResultArrayOutput{Elem: members.Get.ReturnType.Self().ToTyped()}, Module: moduleID},
			Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				keys, err := self.Self().collectionKeys(ctx)
				if err != nil {
					return nil, err
				}
				result := dagql.DynamicResultArrayOutput{Elem: members.Get.ReturnType.Self().ToTyped(), Values: make([]dagql.AnyResult, 0, len(keys))}
				for _, key := range keys {
					var item dagql.AnyResult
					if err := dag.Select(ctx, self, &item, dagql.Selector{Field: "get", Args: []dagql.NamedInput{{Name: "key", Value: key.input}}}); err != nil {
						return nil, err
					}
					result.Values = append(result.Values, item)
				}
				return dagql.NewResultForCurrentCall(ctx, result)
			},
		},
		{
			Spec: &dagql.FieldSpec{Name: "subset", Description: "Select keys from this collection. Preserve author order.", Type: obj, Module: moduleID,
				Args: dagql.NewInputSpecs(dagql.InputSpec{Name: "keys", Type: members.Keys.TypeDef.Self().ToInput()})},
			Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				inputs, ok := args["keys"].(dagql.DynamicArrayInput)
				if !ok {
					return nil, fmt.Errorf("expected an array of collection keys, got %T", args["keys"])
				}
				keys := make([]collectionKey, 0, len(inputs.Values))
				for _, input := range inputs.Values {
					key, err := collectionKeyFromInput(input)
					if err != nil {
						return nil, err
					}
					keys = append(keys, key)
				}
				subset, err := self.Self().collectionSubset(ctx, keys)
				if err != nil {
					return nil, err
				}
				return dagql.NewResultForCurrentCall(ctx, subset)
			},
		},
	}
	if len(obj.TypeDef.Functions) > 1 {
		batch := *obj
		batch.CollectionBatch = true
		if existing, exists := dag.ObjectType(batch.Type().Name()); exists {
			installed, ok := existing.Typed().(*ModuleObject)
			if !ok || !installed.CollectionBatch || installed.TypeDef.Name != obj.TypeDef.Name {
				return nil, fmt.Errorf("collection batch type %q conflicts with an existing type", batch.Type().Name())
			}
		}
		if err := batch.Install(ctx, dag); err != nil {
			return nil, err
		}
		fields = append(fields, dagql.Field[*ModuleObject]{
			Spec: &dagql.FieldSpec{Name: "batch", Description: "Operations on the current collection.", Type: &batch, Module: moduleID},
			Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				batch := *self.Self()
				batch.CollectionBatch = true
				batch.Fields = maps.Clone(batch.Fields)
				return dagql.NewResultForCurrentCall(ctx, &batch)
			},
		})
	}
	return fields, nil
}
