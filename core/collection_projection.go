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
	collectionSubsetName      = "subset"
	collectionBatchFieldName  = "batch"
)

type collectionProjector struct {
	mod               *Module
	objectDefsByName  map[string]*TypeDef
	objectDefCache    map[string]*TypeDef
	batchTypeDefCache map[string]*TypeDef
}

func newCollectionProjector(mod *Module) *collectionProjector {
	projector := &collectionProjector{
		mod:               mod,
		objectDefsByName:  make(map[string]*TypeDef, len(mod.ObjectDefs)),
		objectDefCache:    map[string]*TypeDef{},
		batchTypeDefCache: map[string]*TypeDef{},
	}
	for _, def := range mod.ObjectDefs {
		projector.objectDefsByName[def.AsObject.Value.Name] = def
	}
	return projector
}

func (p *collectionProjector) projectTypeDef(typeDef *TypeDef) *TypeDef {
	if typeDef == nil {
		return nil
	}

	switch typeDef.Kind {
	case TypeDefKindList:
		cp := typeDef.Clone()
		cp.AsList.Value.ElementTypeDef = p.projectTypeDef(cp.AsList.Value.ElementTypeDef)
		return cp
	case TypeDefKindObject:
		if typeDef.AsObject.Valid {
			if rawDef, ok := p.objectDefsByName[typeDef.AsObject.Value.Name]; ok {
				if rawDef.AsCollection.Valid {
					if rawDef == typeDef {
						return p.projectObjectDef(rawDef)
					}
					return p.projectCollectionRef(typeDef, rawDef)
				}
			}
			if len(typeDef.AsObject.Value.Fields) > 0 || len(typeDef.AsObject.Value.Functions) > 0 || typeDef.AsObject.Value.Constructor.Valid {
				cp := typeDef.Clone()
				p.projectObjectMembers(cp.AsObject.Value)
				return cp
			}
		}
		return typeDef.Clone()
	case TypeDefKindInterface:
		cp := typeDef.Clone()
		if cp.AsInterface.Valid {
			for i, fn := range cp.AsInterface.Value.Functions {
				cp.AsInterface.Value.Functions[i] = p.projectFunction(fn)
			}
		}
		return cp
	default:
		return typeDef.Clone()
	}
}

func (p *collectionProjector) projectObjectDef(typeDef *TypeDef) *TypeDef {
	if cached, ok := p.objectDefCache[typeDef.AsObject.Value.Name]; ok {
		return cached.Clone()
	}

	var projected *TypeDef
	if typeDef.AsCollection.Valid {
		projected = p.projectCollectionObjectDef(typeDef)
	} else {
		projected = typeDef.Clone()
		p.projectObjectMembers(projected.AsObject.Value)
	}

	p.objectDefCache[typeDef.AsObject.Value.Name] = projected.Clone()
	return projected
}

func (p *collectionProjector) projectObjectMembers(obj *ObjectTypeDef) {
	for i, field := range obj.Fields {
		obj.Fields[i] = p.projectField(field)
	}
	for i, fn := range obj.Functions {
		obj.Functions[i] = p.projectFunction(fn)
	}
	if obj.Constructor.Valid {
		obj.Constructor.Value = p.projectFunction(obj.Constructor.Value)
	}
}

func (p *collectionProjector) projectField(field *FieldTypeDef) *FieldTypeDef {
	cp := field.Clone()
	cp.TypeDef = p.projectTypeDef(cp.TypeDef)
	return cp
}

func (p *collectionProjector) projectFunction(fn *Function) *Function {
	cp := fn.Clone()
	cp.ReturnType = p.projectTypeDef(cp.ReturnType)
	for i, arg := range cp.Args {
		cp.Args[i] = arg.Clone()
		cp.Args[i].TypeDef = p.projectTypeDef(cp.Args[i].TypeDef)
	}
	return cp
}

func (p *collectionProjector) projectCollectionRef(typeDef *TypeDef, rawDef *TypeDef) *TypeDef {
	cp := typeDef.Clone()
	cp.AsCollection = dagql.NonNull(p.projectCollectionMetadata(rawDef))
	if len(typeDef.AsObject.Value.Fields) > 0 || len(typeDef.AsObject.Value.Functions) > 0 || typeDef.AsObject.Value.Constructor.Valid {
		cp.AsObject.Value = p.projectObjectDef(rawDef).AsObject.Value.Clone()
	}
	return cp
}

func (p *collectionProjector) projectCollectionMetadata(typeDef *TypeDef) *CollectionTypeDef {
	cp := typeDef.AsCollection.Value.Clone()
	cp.KeyType = p.projectTypeDef(cp.KeyType)
	cp.ValueType = p.projectTypeDef(cp.ValueType)
	cp.BatchType = nil
	if batchTypeDef := p.projectCollectionBatchTypeDef(typeDef); batchTypeDef != nil {
		cp.BatchType = p.collectionBatchTypeRef(typeDef)
	}
	return cp
}

func (p *collectionProjector) collectionBatchTypeName(typeDef *TypeDef) string {
	return typeDef.AsObject.Value.Name + "_Batch"
}

func (p *collectionProjector) collectionBatchTypeRef(typeDef *TypeDef) *TypeDef {
	batchTypeDef := p.projectCollectionBatchTypeDef(typeDef)
	if batchTypeDef == nil {
		return nil
	}
	return (&TypeDef{}).WithObject(
		batchTypeDef.AsObject.Value.Name,
		batchTypeDef.AsObject.Value.Description,
		batchTypeDef.AsObject.Value.Deprecated,
		nullableSourceMapPtr(batchTypeDef.AsObject.Value.SourceMap),
	)
}

func (p *collectionProjector) projectCollectionBatchTypeDef(typeDef *TypeDef) *TypeDef {
	if !typeDef.AsCollection.Valid {
		return nil
	}
	typeName := p.collectionBatchTypeName(typeDef)
	if cached, ok := p.batchTypeDefCache[typeName]; ok {
		return cached.Clone()
	}

	batchFns := p.collectionBatchFunctions(typeDef)
	if len(batchFns) == 0 {
		return nil
	}

	rawObj := typeDef.AsObject.Value
	batchObj := NewObjectTypeDef(typeName, "Type-specific efficient operations over the current subset.", nil).WithSourceMap(nullableSourceMapPtr(rawObj.SourceMap))
	batchObj.OriginalName = typeName
	for _, fn := range batchFns {
		batchObj.Functions = append(batchObj.Functions, p.projectFunction(fn))
	}

	batchTypeDef := &TypeDef{
		Kind:     TypeDefKindObject,
		AsObject: dagql.NonNull(batchObj),
	}
	p.batchTypeDefCache[typeName] = batchTypeDef.Clone()
	return batchTypeDef
}

func (p *collectionProjector) projectCollectionObjectDef(typeDef *TypeDef) *TypeDef {
	rawObj := typeDef.AsObject.Value
	collection := typeDef.AsCollection.Value

	projectedObj := rawObj.Clone()
	projectedObj.Fields = nil
	projectedObj.Functions = nil
	projectedObj.Constructor = dagql.Null[*Function]()

	keysField, ok := rawObj.FieldByName(collection.KeysFieldName)
	if !ok {
		panic(fmt.Sprintf("collection keys field %q missing from %q", collection.KeysFieldName, rawObj.Name))
	}
	projectedKeys := p.projectField(keysField)
	projectedKeys.Name = collectionKeysFieldName
	projectedKeys.OriginalName = collectionKeysFieldName
	projectedObj.Fields = append(projectedObj.Fields, projectedKeys)

	projectedObj.Fields = append(projectedObj.Fields, &FieldTypeDef{
		Name:         collectionListFieldName,
		OriginalName: collectionListFieldName,
		Description:  "Items in the current subset, in the same order as `keys`.",
		TypeDef:      (&TypeDef{}).WithListOf(p.projectTypeDef(collection.ValueType)),
	})

	getFn, ok := rawObj.FunctionByName(collection.GetFunctionName)
	if !ok {
		panic(fmt.Sprintf("collection get function %q missing from %q", collection.GetFunctionName, rawObj.Name))
	}
	projectedGet := p.projectFunction(getFn)
	projectedGet.Name = collectionGetFunctionName
	projectedGet.OriginalName = collectionGetFunctionName
	projectedObj.Functions = append(projectedObj.Functions, projectedGet)

	projectedObj.Functions = append(projectedObj.Functions, &Function{
		Name:         collectionSubsetName,
		OriginalName: collectionSubsetName,
		Description:  "Restrict the collection to an exact subset of keys.",
		Args: []*FunctionArg{
			{
				Name:         collectionKeysFieldName,
				OriginalName: collectionKeysFieldName,
				Description:  "Keys to retain from the current subset.",
				TypeDef:      (&TypeDef{}).WithListOf(p.projectTypeDef(collection.KeyType)),
			},
		},
		ReturnType: &TypeDef{
			Kind: TypeDefKindObject,
			AsObject: dagql.NonNull(&ObjectTypeDef{
				Name:         rawObj.Name,
				OriginalName: rawObj.OriginalName,
			}),
			AsCollection: dagql.NonNull(p.projectCollectionMetadata(typeDef)),
		},
	})

	if batchTypeRef := p.collectionBatchTypeRef(typeDef); batchTypeRef != nil {
		projectedObj.Fields = append(projectedObj.Fields, &FieldTypeDef{
			Name:         collectionBatchFieldName,
			OriginalName: collectionBatchFieldName,
			Description:  "Type-specific efficient operations over the current subset.",
			TypeDef:      batchTypeRef,
		})
	}

	return &TypeDef{
		Kind:         TypeDefKindObject,
		AsObject:     dagql.NonNull(projectedObj),
		AsCollection: dagql.NonNull(p.projectCollectionMetadata(typeDef)),
	}
}

func (p *collectionProjector) collectionBatchFunctions(typeDef *TypeDef) []*Function {
	if !typeDef.AsCollection.Valid {
		return nil
	}
	collection := typeDef.AsCollection.Value
	fns := make([]*Function, 0, len(typeDef.AsObject.Value.Functions))
	for _, fn := range typeDef.AsObject.Value.Functions {
		if fn.Name == collection.GetFunctionName {
			continue
		}
		fns = append(fns, fn)
	}
	return fns
}

type CollectionBatchObject struct {
	Module         *Module
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
		def.Directives = append(def.Directives, obj.TypeDef.SourceMap.Value.TypeDirective())
	}
	return def
}

func (obj *CollectionBatchObject) Install(ctx context.Context, dag *dagql.Server) error {
	classOpts := dagql.ClassOpts[*CollectionBatchObject]{
		Typed: obj,
	}

	installDirectives := []*ast.Directive{}
	if obj.TypeDef.SourceMap.Valid {
		classOpts.SourceMap = obj.TypeDef.SourceMap.Value.TypeDirective()
		installDirectives = append(installDirectives, obj.TypeDef.SourceMap.Value.TypeDirective())
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
	projector := newCollectionProjector(obj.Module)
	backingTypeDef := &TypeDef{
		Kind:         TypeDefKindObject,
		AsObject:     dagql.NonNull(obj.BackingTypeDef),
		AsCollection: dagql.NonNull(obj.Collection),
	}
	batchFns := projector.collectionBatchFunctions(backingTypeDef)
	fields := make([]dagql.Field[*CollectionBatchObject], 0, len(batchFns))
	for _, fn := range batchFns {
		modFun, err := NewModFunction(ctx, obj.Module, obj.BackingTypeDef, fn)
		if err != nil {
			return nil, fmt.Errorf("failed to create batch function %q: %w", fn.Name, err)
		}
		if err := modFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
			return nil, fmt.Errorf("failed to merge user defaults for %q: %w", fn.Name, err)
		}
		spec, err := fn.FieldSpec(ctx, obj.Module)
		if err != nil {
			return nil, fmt.Errorf("failed to get field spec for batch function %q: %w", fn.Name, err)
		}
		spec.Module = obj.Module.IDModule()
		spec.GetCacheConfig = modFun.CacheConfigForCall

		fields = append(fields, dagql.Field[*CollectionBatchObject]{
			Spec: &spec,
			Func: func(ctx context.Context, batch dagql.ObjectResult[*CollectionBatchObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				parent, err := dagql.NewResultForCurrentID(ctx, &ModuleObject{
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

func (obj *ModuleObject) collectionMembers(ctx context.Context, dag *dagql.Server) ([]dagql.Field[*ModuleObject], error) {
	backingTypeDef := &TypeDef{
		Kind:         TypeDefKindObject,
		AsObject:     dagql.NonNull(obj.TypeDef),
		AsCollection: dagql.NonNull(obj.Collection),
	}
	projector := newCollectionProjector(obj.Module)
	if batchTypeDef := projector.projectCollectionBatchTypeDef(backingTypeDef); batchTypeDef != nil {
		batchObj := &CollectionBatchObject{
			Module:         obj.Module,
			TypeDef:        batchTypeDef.AsObject.Value,
			BackingTypeDef: obj.TypeDef,
			Collection:     obj.Collection,
		}
		if err := batchObj.Install(ctx, dag); err != nil {
			return nil, err
		}
	}

	keysField, ok := obj.TypeDef.FieldByName(obj.Collection.KeysFieldName)
	if !ok {
		return nil, fmt.Errorf("collection keys field %q not found on %q", obj.Collection.KeysFieldName, obj.TypeDef.Name)
	}
	getFn, ok := obj.TypeDef.FunctionByName(obj.Collection.GetFunctionName)
	if !ok {
		return nil, fmt.Errorf("collection get function %q not found on %q", obj.Collection.GetFunctionName, obj.TypeDef.Name)
	}

	keyModType, ok, err := obj.Module.ModTypeFor(ctx, obj.Collection.KeyType, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("could not resolve key type %s", obj.Collection.KeyType.ToType())
	}

	getModFun, err := NewModFunction(ctx, obj.Module, obj.TypeDef, getFn)
	if err != nil {
		return nil, fmt.Errorf("failed to create get function: %w", err)
	}
	if err := getModFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return nil, fmt.Errorf("failed to merge user defaults for get: %w", err)
	}

	projectedGetFn := projector.projectFunction(getFn)
	projectedGetFn.Name = collectionGetFunctionName
	projectedGetFn.OriginalName = collectionGetFunctionName
	getSpec, err := projectedGetFn.FieldSpec(ctx, obj.Module)
	if err != nil {
		return nil, fmt.Errorf("failed to build get field spec: %w", err)
	}
	getSpec.Module = obj.Module.IDModule()
	getSpec.GetCacheConfig = getModFun.CacheConfigForCall

	fields := []dagql.Field[*ModuleObject]{
		obj.collectionKeysField(keysField),
		obj.collectionListField(getModFun, dag),
		{
			Spec: &getSpec,
			Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				keyInput, ok := args[getFn.Args[0].Name]
				if !ok {
					return nil, fmt.Errorf("missing collection key argument %q", getFn.Args[0].Name)
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
		obj.collectionSubsetField(keyModType),
	}

	if batchTypeDef := projector.projectCollectionBatchTypeDef(backingTypeDef); batchTypeDef != nil {
		fields = append(fields, obj.collectionBatchField(batchTypeDef.AsObject.Value))
	}

	return fields, nil
}

func (obj *ModuleObject) collectionKeysField(keysField *FieldTypeDef) dagql.Field[*ModuleObject] {
	spec := &dagql.FieldSpec{
		Name:             collectionKeysFieldName,
		Description:      formatGqlDescription(keysField.Description),
		Type:             keysField.TypeDef.ToTyped(),
		Module:           obj.Module.IDModule(),
		GetCacheConfig:   obj.Module.CacheConfigForCall,
		DeprecatedReason: keysField.Deprecated,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{Name: trivialFieldDirectiveName})
	if keysField.SourceMap.Valid {
		spec.Directives = append(spec.Directives, keysField.SourceMap.Value.TypeDirective())
	}

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			modType, ok, err := obj.Module.ModTypeFor(ctx, keysField.TypeDef, true)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("could not resolve keys field type %s", keysField.TypeDef.ToType())
			}
			return modType.ConvertFromSDKResult(ctx, self.Self().Fields[keysField.OriginalName])
		},
	}
}

func (obj *ModuleObject) collectionListField(getModFun *ModuleFunction, dag *dagql.Server) dagql.Field[*ModuleObject] {
	listType := (&TypeDef{}).WithListOf(obj.Collection.ValueType.Clone())
	spec := &dagql.FieldSpec{
		Name:           collectionListFieldName,
		Description:    "Items in the current subset, in the same order as `keys`.",
		Type:           listType.ToTyped(),
		Module:         obj.Module.IDModule(),
		GetCacheConfig: obj.Module.CacheConfigForCall,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{Name: trivialFieldDirectiveName})

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			keys, err := self.Self().collectionKeys()
			if err != nil {
				return nil, err
			}
			result := dagql.DynamicResultArrayOutput{
				Elem: obj.Collection.ValueType.ToTyped(),
			}
			result.Values = make([]dagql.AnyResult, 0, len(keys))
			for i, key := range keys {
				keyInput, err := self.Self().collectionKeyInput(key)
				if err != nil {
					return nil, err
				}
				itemCtx := dagql.ContextWithID(ctx, dagql.CurrentID(ctx).SelectNth(i+1))
				item, err := self.Self().callCollectionGet(itemCtx, self, getModFun, dag, keyInput)
				if err != nil {
					return nil, err
				}
				result.Values = append(result.Values, item)
			}
			return dagql.NewResultForCurrentID(ctx, result)
		},
	}
}

func (obj *ModuleObject) collectionSubsetField(keyModType ModType) dagql.Field[*ModuleObject] {
	keysArgType := (&TypeDef{}).WithListOf(obj.Collection.KeyType.Clone())
	spec := &dagql.FieldSpec{
		Name:        collectionSubsetName,
		Description: "Restrict the collection to an exact subset of keys.",
		Type:        obj,
		Module:      obj.Module.IDModule(),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name:        collectionKeysFieldName,
				Description: "Keys to retain from the current subset.",
				Type:        keysArgType.ToInput(),
			},
		),
		GetCacheConfig: obj.Module.CacheConfigForCall,
	}

	listKeyModType := &ListType{
		Elem:       obj.Collection.KeyType.Clone(),
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
			keysField, ok := self.Self().TypeDef.FieldByName(self.Self().Collection.KeysFieldName)
			if !ok {
				return nil, fmt.Errorf("collection keys field %q not found on %q", self.Self().Collection.KeysFieldName, self.Self().TypeDef.Name)
			}
			fields[keysField.OriginalName] = orderedKeys
			return dagql.NewResultForCurrentID(ctx, &ModuleObject{
				Module:     self.Self().Module,
				TypeDef:    self.Self().TypeDef,
				Collection: self.Self().Collection,
				Fields:     fields,
			})
		},
	}
}

func (obj *ModuleObject) collectionBatchField(batchTypeDef *ObjectTypeDef) dagql.Field[*ModuleObject] {
	spec := &dagql.FieldSpec{
		Name:           collectionBatchFieldName,
		Description:    "Type-specific efficient operations over the current subset.",
		Type:           &CollectionBatchObject{TypeDef: batchTypeDef},
		Module:         obj.Module.IDModule(),
		GetCacheConfig: obj.Module.CacheConfigForCall,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{Name: trivialFieldDirectiveName})

	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			return dagql.NewResultForCurrentID(ctx, &CollectionBatchObject{
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
	keysField, ok := obj.TypeDef.FieldByName(obj.Collection.KeysFieldName)
	if !ok {
		return nil, fmt.Errorf("collection keys field %q not found on %q", obj.Collection.KeysFieldName, obj.TypeDef.Name)
	}
	return collectionSliceValues(obj.Fields[keysField.OriginalName])
}

func (obj *ModuleObject) collectionKeyInput(rawKey any) (dagql.Input, error) {
	return obj.Collection.KeyType.ToInput().Decoder().DecodeInput(rawKey)
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

func nullableSourceMapPtr(sourceMap dagql.Nullable[*SourceMap]) *SourceMap {
	if !sourceMap.Valid {
		return nil
	}
	return sourceMap.Value.Clone()
}
