package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

const (
	collectionKeysFieldName   = "keys"
	collectionListFieldName   = "list"
	collectionGetFunctionName = "get"
	collectionGetArgName      = "key"
	collectionSubsetName      = "subset"
	collectionBatchFieldName  = "batch"
)

type CollectionBatchObject struct {
	Module         dagql.ObjectResult[*Module]
	TypeDef        *ObjectTypeDef
	BackingTypeDef *ObjectTypeDef
	Collection     *CollectionTypeDef
	Fields         map[string]any
}

func (obj *CollectionBatchObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: obj.TypeDef.Name,
		NonNull:   true,
	}
}

func (obj *CollectionBatchObject) TypeDescription() string {
	return formatGqlDescription(obj.TypeDef.Description)
}

func (obj *CollectionBatchObject) TypeDefinition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Object,
		Name: obj.Type().Name(),
	}
	if obj.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, obj.TypeDef.SourceMap.Value.Self().TypeDirective())
	}
	return def
}

func (obj *CollectionBatchObject) Install(ctx context.Context, dag *dagql.Server) error {
	classOpts := dagql.ClassOpts[*CollectionBatchObject]{
		Typed: obj,
	}

	installDirectives := []*ast.Directive{}
	if obj.TypeDef.SourceMap.Valid {
		classOpts.SourceMap = obj.TypeDef.SourceMap.Value.Self().TypeDirective()
		installDirectives = append(installDirectives, obj.TypeDef.SourceMap.Value.Self().TypeDirective())
	}

	class := dagql.NewClass(dag, classOpts)
	fields, err := obj.functions(ctx, dag)
	if err != nil {
		return err
	}
	class.Install(fields...)
	dag.InstallObject(class, installDirectives...)
	return nil
}

func (obj *CollectionBatchObject) functions(ctx context.Context, dag *dagql.Server) ([]dagql.Field[*CollectionBatchObject], error) {
	batchFns := collectionBatchFunctionResults(obj.BackingTypeDef, obj.Collection)
	fields := make([]dagql.Field[*CollectionBatchObject], 0, len(batchFns))
	for _, fnRes := range batchFns {
		fn := fnRes.Self()
		if fn == nil {
			continue
		}
		modFun, err := NewModFunction(ctx, obj.Module, obj.BackingTypeDef, fn)
		if err != nil {
			return nil, fmt.Errorf("failed to create batch function %q: %w", fn.Name, err)
		}
		if err := modFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
			return nil, fmt.Errorf("failed to merge user defaults for %q: %w", fn.Name, err)
		}
		spec, err := modFun.metadata.FieldSpec(ctx, NewUserMod(obj.Module))
		if err != nil {
			return nil, fmt.Errorf("failed to get field spec for batch function %q: %w", fn.Name, err)
		}
		moduleID, err := NewUserMod(obj.Module).ResultCallModule(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve module identity for batch function %q: %w", fn.Name, err)
		}
		spec.Module = moduleID
		spec.GetDynamicInput = modFun.DynamicInputsForCall
		spec.ImplicitInputs = append(spec.ImplicitInputs, modFun.cacheImplicitInputs()...)

		fields = append(fields, dagql.Field[*CollectionBatchObject]{
			Spec: &spec,
			Func: func(ctx context.Context, batch dagql.ObjectResult[*CollectionBatchObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				parent, err := dagql.NewObjectResultForCurrentCall(ctx, dag, &ModuleObject{
					Module:     batch.Self().Module,
					TypeDef:    batch.Self().BackingTypeDef,
					Collection: batch.Self().Collection,
					Fields:     batch.Self().Fields,
				})
				if err != nil {
					return nil, err
				}

				opts := &CallOpts{
					ParentTyped:  parent,
					ParentFields: batch.Self().Fields,
					Server:       dag,
				}
				for name, val := range args {
					opts.Inputs = append(opts.Inputs, CallInput{
						Name:  name,
						Value: val,
					})
				}
				slices.SortFunc(opts.Inputs, func(a, b CallInput) int {
					switch {
					case a.Name < b.Name:
						return -1
					case a.Name > b.Name:
						return 1
					default:
						return 0
					}
				})
				return modFun.Call(ctx, opts)
			},
		})
	}
	return fields, nil
}

// collectionBacking returns the raw module-defined object type definition,
// which carries the SDK's real field and function names for dispatch. Falls
// back to the public type definition for collections built before projection.
func (obj *ModuleObject) collectionBacking() *ObjectTypeDef {
	if backing := obj.Collection.Backing(); backing != nil {
		return backing
	}
	return obj.TypeDef
}

func (obj *ModuleObject) collectionMembers(ctx context.Context, dag *dagql.Server) ([]dagql.Field[*ModuleObject], error) {
	backing := obj.collectionBacking()
	if batchTypeDef := collectionBatchTypeDef(backing, obj.Collection); batchTypeDef != nil {
		batchObj := &CollectionBatchObject{
			Module:         obj.Module,
			TypeDef:        batchTypeDef,
			BackingTypeDef: backing,
			Collection:     obj.Collection,
		}
		if err := batchObj.Install(ctx, dag); err != nil {
			return nil, err
		}
	}

	keysField, ok := backing.FieldByName(obj.Collection.KeysFieldName)
	if !ok {
		return nil, fmt.Errorf("collection keys field %q not found on %q", obj.Collection.KeysFieldName, backing.Name)
	}
	getFn, ok := backing.FunctionByName(obj.Collection.GetFunctionName)
	if !ok {
		return nil, fmt.Errorf("collection get function %q not found on %q", obj.Collection.GetFunctionName, backing.Name)
	}

	keyModType, ok, err := NewUserMod(obj.Module).ModTypeFor(ctx, obj.Collection.KeyType.Value.Self(), true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("could not resolve key type %s", obj.Collection.KeyType.Value.Self().ToType())
	}

	getModFun, err := NewModFunction(ctx, obj.Module, backing, getFn)
	if err != nil {
		return nil, fmt.Errorf("failed to create get function: %w", err)
	}
	if err := getModFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return nil, fmt.Errorf("failed to merge user defaults for get: %w", err)
	}

	projectedGet := getFn.Clone()
	projectedGet.Name = collectionGetFunctionName
	projectedGet.OriginalName = collectionGetFunctionName
	projectedGet.Args = nil
	getSpec, err := projectedGet.FieldSpec(ctx, NewUserMod(obj.Module))
	if err != nil {
		return nil, fmt.Errorf("failed to build get field spec: %w", err)
	}
	moduleID, err := NewUserMod(obj.Module).ResultCallModule(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module identity for get: %w", err)
	}
	getSpec.Module = moduleID
	getSpec.GetDynamicInput = getModFun.DynamicInputsForCall
	getSpec.ImplicitInputs = append(getSpec.ImplicitInputs, getModFun.cacheImplicitInputs()...)
	getArg := getFn.Args[0].Self()
	getArgSpec := dagql.InputSpec{
		Name:             collectionGetArgName,
		Description:      formatGqlDescription(getArg.Description),
		Type:             obj.Collection.KeyType.Value.Self().ToInput(),
		DeprecatedReason: getArg.Deprecated,
	}
	if getArg.SourceMap.Valid && getArg.SourceMap.Value.Self() != nil {
		getArgSpec.Directives = append(getArgSpec.Directives, getArg.SourceMap.Value.Self().TypeDirective())
	}
	getArgSpec.Directives = append(getArgSpec.Directives, getArg.Directives()...)
	getSpec.Args.Add(getArgSpec)

	fields := []dagql.Field[*ModuleObject]{
		obj.collectionKeysField(keysField, moduleID),
		obj.collectionListField(getModFun, dag, moduleID),
		{
			Spec: &getSpec,
			Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				keyInput, ok := args[collectionGetArgName]
				if !ok {
					return nil, fmt.Errorf("missing collection key argument %q", collectionGetArgName)
				}
				typedKey, ok := keyInput.(dagql.Typed)
				if !ok {
					return nil, fmt.Errorf("unexpected key input type %T", keyInput)
				}
				rawKey, err := keyModType.ConvertToSDKInput(ctx, typedKey)
				if err != nil {
					return nil, err
				}
				if err := self.Self().validateCollectionKey(rawKey); err != nil {
					return nil, err
				}
				return self.Self().callCollectionGet(ctx, self, getModFun, dag, keyInput)
			},
		},
		obj.collectionSubsetField(keyModType, moduleID),
	}

	if batchTypeDef := collectionBatchTypeDef(backing, obj.Collection); batchTypeDef != nil {
		fields = append(fields, obj.collectionBatchField(batchTypeDef, dag, moduleID))
	}
	return fields, nil
}

func (obj *ModuleObject) collectionKeysField(keysField *FieldTypeDef, moduleID *dagql.ResultCallModule) dagql.Field[*ModuleObject] {
	spec := &dagql.FieldSpec{
		Name:             collectionKeysFieldName,
		Description:      keysField.Description,
		Type:             keysField.TypeDef.Self().ToTyped(),
		Module:           moduleID,
		DeprecatedReason: keysField.Deprecated,
		Trivial:          true,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{Name: trivialFieldDirectiveName})
	if keysField.SourceMap.Valid {
		spec.Directives = append(spec.Directives, keysField.SourceMap.Value.Self().TypeDirective())
	}

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			modType, ok, err := NewUserMod(obj.Module).ModTypeFor(ctx, keysField.TypeDef.Self(), true)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("could not resolve keys field type %s", keysField.TypeDef.Self().ToType())
			}
			return modType.ConvertFromSDKResult(ctx, self.Self().Fields[keysField.OriginalName])
		},
	}
}

func (obj *ModuleObject) collectionListField(getModFun *ModuleFunction, dag *dagql.Server, moduleID *dagql.ResultCallModule) dagql.Field[*ModuleObject] {
	listType := dagql.DynamicResultArrayOutput{
		Elem: obj.Collection.ValueType.Value.Self().ToTyped(),
	}
	spec := &dagql.FieldSpec{
		Name:        collectionListFieldName,
		Description: "Items in the current subset, in the same order as keys.",
		Type:        listType,
		Module:      moduleID,
	}

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			keys, err := self.Self().collectionKeys()
			if err != nil {
				return nil, err
			}
			result := dagql.DynamicResultArrayOutput{
				Elem: obj.Collection.ValueType.Value.Self().ToTyped(),
			}
			result.Values = make([]dagql.AnyResult, 0, len(keys))
			for _, key := range keys {
				keyInput, err := self.Self().collectionKeyInput(key)
				if err != nil {
					return nil, err
				}
				item, err := self.Self().callCollectionGet(ctx, self, getModFun, dag, keyInput)
				if err != nil {
					return nil, err
				}
				result.Values = append(result.Values, item)
			}
			return dagql.NewResultForCurrentCall(ctx, result)
		},
	}
}

func (obj *ModuleObject) collectionSubsetField(keyModType ModType, moduleID *dagql.ResultCallModule) dagql.Field[*ModuleObject] {
	keysArgType := dagql.DynamicArrayInput{
		Elem: obj.Collection.KeyType.Value.Self().ToInput(),
	}
	spec := &dagql.FieldSpec{
		Name:        collectionSubsetName,
		Description: "Restrict the collection to an exact subset of keys.",
		Type:        obj,
		Module:      moduleID,
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name:        collectionKeysFieldName,
				Description: "Keys to retain from the current subset.",
				Type:        keysArgType,
			},
		),
	}

	listKeyModType := &ListType{
		Elem:       obj.Collection.KeyType.Value,
		Underlying: keyModType,
	}

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			keysInput, ok := args[collectionKeysFieldName]
			if !ok {
				return nil, fmt.Errorf("missing subset keys")
			}
			typedKeys, ok := keysInput.(dagql.Typed)
			if !ok {
				return nil, fmt.Errorf("unexpected subset keys input type %T", keysInput)
			}
			rawSubset, err := listKeyModType.ConvertToSDKInput(ctx, typedKeys)
			if err != nil {
				return nil, err
			}
			subsetKeys, err := collectionSliceValues(rawSubset)
			if err != nil {
				return nil, err
			}
			orderedKeys, err := self.Self().collectionSubsetKeys(subsetKeys)
			if err != nil {
				return nil, err
			}

			fields := maps.Clone(self.Self().Fields)
			keysField, ok := self.Self().collectionBacking().FieldByName(self.Self().Collection.KeysFieldName)
			if !ok {
				return nil, fmt.Errorf("collection keys field %q not found on %q", self.Self().Collection.KeysFieldName, self.Self().TypeDef.Name)
			}
			fields[keysField.OriginalName] = orderedKeys
			return dagql.NewResultForCurrentCall(ctx, &ModuleObject{
				Module:     self.Self().Module,
				TypeDef:    self.Self().TypeDef,
				Collection: self.Self().Collection,
				Fields:     fields,
			})
		},
	}
}

func (obj *ModuleObject) collectionBatchField(batchTypeDef *ObjectTypeDef, dag *dagql.Server, moduleID *dagql.ResultCallModule) dagql.Field[*ModuleObject] {
	spec := &dagql.FieldSpec{
		Name:        collectionBatchFieldName,
		Description: "Type-specific efficient operations over the current subset.",
		Type:        &CollectionBatchObject{TypeDef: batchTypeDef},
		Module:      moduleID,
		Trivial:     true,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{Name: trivialFieldDirectiveName})

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			return dagql.NewObjectResultForCurrentCall(ctx, dag, &CollectionBatchObject{
				Module:         self.Self().Module,
				TypeDef:        batchTypeDef,
				BackingTypeDef: self.Self().TypeDef,
				Collection:     self.Self().Collection,
				Fields:         maps.Clone(self.Self().Fields),
			})
		},
	}
}

func (obj *ModuleObject) collectionKeys() ([]any, error) {
	keysField, ok := obj.collectionBacking().FieldByName(obj.Collection.KeysFieldName)
	if !ok {
		return nil, fmt.Errorf("collection keys field %q not found on %q", obj.Collection.KeysFieldName, obj.TypeDef.Name)
	}
	return collectionSliceValues(obj.Fields[keysField.OriginalName])
}

func (obj *ModuleObject) collectionKeyInput(rawKey any) (dagql.Input, error) {
	return obj.Collection.KeyType.Value.Self().ToInput().Decoder().DecodeInput(rawKey)
}

func (obj *ModuleObject) validateCollectionKey(rawKey any) error {
	currentKeys, err := obj.collectionKeys()
	if err != nil {
		return err
	}
	keyID, err := collectionKeyID(rawKey)
	if err != nil {
		return err
	}
	for _, currentKey := range currentKeys {
		currentKeyID, err := collectionKeyID(currentKey)
		if err != nil {
			return err
		}
		if currentKeyID == keyID {
			return nil
		}
	}
	return fmt.Errorf("collection %q does not contain key %s in the current subset", obj.TypeDef.Name, keyID)
}

func (obj *ModuleObject) collectionSubsetKeys(subsetKeys []any) ([]any, error) {
	currentKeys, err := obj.collectionKeys()
	if err != nil {
		return nil, err
	}

	selected := make(map[string]struct{}, len(subsetKeys))
	for _, key := range subsetKeys {
		keyID, err := collectionKeyID(key)
		if err != nil {
			return nil, err
		}
		if _, exists := selected[keyID]; exists {
			return nil, fmt.Errorf("collection %q subset contains duplicate key %s", obj.TypeDef.Name, keyID)
		}
		selected[keyID] = struct{}{}
	}

	ordered := make([]any, 0, len(subsetKeys))
	found := make(map[string]struct{}, len(subsetKeys))
	for _, key := range currentKeys {
		keyID, err := collectionKeyID(key)
		if err != nil {
			return nil, err
		}
		if _, keep := selected[keyID]; keep {
			ordered = append(ordered, key)
			found[keyID] = struct{}{}
		}
	}

	for keyID := range selected {
		if _, ok := found[keyID]; !ok {
			return nil, fmt.Errorf("collection %q does not contain key %s in the current subset", obj.TypeDef.Name, keyID)
		}
	}
	return ordered, nil
}

func (obj *ModuleObject) callCollectionGet(
	ctx context.Context,
	parent dagql.AnyResult,
	getModFun *ModuleFunction,
	dag *dagql.Server,
	keyInput dagql.Input,
) (dagql.AnyResult, error) {
	return getModFun.Call(ctx, &CallOpts{
		Inputs: []CallInput{{
			Name:  obj.Collection.GetArgName,
			Value: keyInput,
		}},
		ParentTyped:  parent,
		ParentFields: obj.Fields,
		Server:       dag,
	})
}

func collectionSliceValues(value any) ([]any, error) {
	if value == nil {
		return []any{}, nil
	}
	if values, ok := value.([]any); ok {
		return slices.Clone(values), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var values []any
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func collectionKeyID(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func collectionBatchTypeDef(backing *ObjectTypeDef, collection *CollectionTypeDef) *ObjectTypeDef {
	batchFns := collectionBatchFunctionResults(backing, collection)
	if len(batchFns) == 0 {
		return nil
	}
	// No separator: GraphQL object name normalization strips underscores, so
	// a separator would make the type definition name drift from the name
	// the schema (and generated clients) actually use.
	return &ObjectTypeDef{
		Name:         backing.Name + "Batch",
		OriginalName: backing.OriginalName + "Batch",
		Description:  "Type-specific efficient operations over the current subset.",
		Functions:    append(dagql.ObjectResultArray[*Function](nil), batchFns...),
	}
}

func collectionBatchFunctionResults(backing *ObjectTypeDef, collection *CollectionTypeDef) dagql.ObjectResultArray[*Function] {
	if backing == nil || collection == nil {
		return nil
	}
	fns := make(dagql.ObjectResultArray[*Function], 0, len(backing.Functions))
	for _, fn := range backing.Functions {
		if fn.Self() == nil || fn.Self().Name == collection.GetFunctionName {
			continue
		}
		fns = append(fns, fn)
	}
	return fns
}
