package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

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
	mod     *Module
}

var _ ModType = &ModuleObjectType{}

func (t *ModuleObjectType) SourceMod() Mod {
	if t.mod == nil {
		return nil
	}
	return t.mod
}

func (t *ModuleObjectType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("ModuleObjectType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	switch value := value.(type) {
	case map[string]any:
		return dagql.NewResultForCurrentID(ctx, &ModuleObject{
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
	case DynamicID:
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get current query: %w", err)
		}
		deps, err := query.IDDeps(ctx, x.ID())
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
		case dagql.ObjectResult[*ModuleObject]:
			return x.Self().Fields, nil
		case dagql.ObjectResult[*InterfaceAnnotatedValue]:
			return x.Self().Fields, nil
		default:
			return nil, fmt.Errorf("unexpected value type %T", x)
		}
	default:
		return nil, fmt.Errorf("%T.ConvertToSDKInput cannot handle %T", t, x)
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

	// Iterate fields in sorted order to produce a deterministic hash.
	for _, k := range slices.Sorted(maps.Keys(objFields)) {
		v := objFields[k]
		fieldTypeDef, ok := t.typeDef.FieldByOriginalName(k)
		if !ok {
			// this is a private field; do best-effort collection, because we don't
			// have type hints for these, but the user may still store IDs in them
			if err := content.CollectKeyed(k, func() error {
				return content.CollectUnknown(v)
			}); err != nil {
				return err
			}
			continue
		}

		modType, ok, err := t.mod.ModTypeFor(ctx, fieldTypeDef.TypeDef, true)
		if err != nil {
			return fmt.Errorf("failed to get mod type for field %q: %w", k, err)
		}
		if !ok {
			return fmt.Errorf("could not find mod type for field %q", k)
		}

		curID := value.ID()
		fieldID := curID.Append(
			fieldTypeDef.TypeDef.ToType(),
			fieldTypeDef.Name,
			call.WithView(curID.View()),
			call.WithModule(curID.Module()),
		)
		ctx := dagql.ContextWithID(ctx, fieldID)

		typed, err := modType.ConvertFromSDKResult(ctx, v)
		if err != nil {
			return fmt.Errorf("failed to convert field %q: %w", k, err)
		}
		if err := content.CollectKeyed(k, func() error {
			return modType.CollectContent(ctx, typed, content)
		}); err != nil {
			return fmt.Errorf("failed to collect content for field %q: %w", k, err)
		}
	}

	return nil
}

func (t *ModuleObjectType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind:     TypeDefKindObject,
		AsObject: dagql.NonNull(t.typeDef),
	}
}

type Callable interface {
	Call(context.Context, *CallOpts) (dagql.AnyResult, error)
	ReturnType() (ModType, error)
	ArgType(argName string) (ModType, error)
	CacheConfigForCall(context.Context, dagql.AnyResult, map[string]dagql.Input, call.View, dagql.GetCacheConfigRequest) (*dagql.GetCacheConfigResponse, error)
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
		return NewModFunction(
			ctx,
			mod,
			t.typeDef,
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

func (obj *ModuleObject) TypeDescription() string {
	return formatGqlDescription(obj.TypeDef.Description)
}

func (obj *ModuleObject) TypeDefinition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Object,
		Name: obj.Type().Name(),
	}
	if obj.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, obj.TypeDef.SourceMap.Value.TypeDirective())
	}
	return def
}

func (obj *ModuleObject) Install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error {
	if obj.Module.ResultID == nil {
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
		classOpts.SourceMap = obj.TypeDef.SourceMap.Value.TypeDirective()
		installDirectives = append(installDirectives, obj.TypeDef.SourceMap.Value.TypeDirective())
	}

	class := dagql.NewClass(dag, classOpts)
	if obj.isMainObject() && !opt.SkipConstructor {
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
	dag.InstallObject(class, installDirectives...)

	if obj.isMainObject() && opt.Entrypoint {
		if err := obj.installEntrypointMethods(ctx, dag); err != nil {
			return fmt.Errorf("failed to install entrypoint methods: %w", err)
		}
	}

	return nil
}

func (obj *ModuleObject) isMainObject() bool {
	return gqlObjectName(obj.TypeDef.OriginalName) == gqlObjectName(obj.Module.OriginalName)
}

func (obj *ModuleObject) installConstructor(ctx context.Context, dag *dagql.Server) error {
	objDef := obj.TypeDef
	mod := obj.Module

	// if no constructor defined, install a basic one that initializes an empty object
	if !objDef.Constructor.Valid {
		spec := dagql.FieldSpec{
			Name:             gqlFieldName(mod.Name()),
			Description:      formatGqlDescription(objDef.Description),
			Type:             obj,
			Module:           obj.Module.IDModule(),
			GetCacheConfig:   mod.CacheConfigForCall,
			DeprecatedReason: objDef.Deprecated,
		}

		if objDef.SourceMap.Valid {
			spec.Directives = append(spec.Directives, objDef.SourceMap.Value.TypeDirective())
		}

		dag.Root().ObjectType().Extend(
			spec,
			func(ctx context.Context, self dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentID(ctx, &ModuleObject{
					Module:  mod,
					TypeDef: objDef,
					Fields:  map[string]any{},
				})
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

	fn, err := NewModFunction(ctx, mod, objDef, fnTypeDef)
	if err != nil {
		return fmt.Errorf("failed to create function: %w", err)
	}
	if err := fn.mergeUserDefaultsTypeDefs(ctx); err != nil {
		return fmt.Errorf("failed to merge user defaults: %w", err)
	}
	spec, err := fn.metadata.FieldSpec(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to get field spec for constructor: %w", err)
	}
	spec.Name = gqlFieldName(mod.Name())
	spec.Module = obj.Module.IDModule()
	spec.GetCacheConfig = fn.CacheConfigForCall

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
	constructorName := gqlFieldName(obj.Module.Name())
	constructorSpec, ok := dag.Root().ObjectType().FieldSpec(constructorName, dag.View)
	if !ok {
		return fmt.Errorf("constructor %q not found", constructorName)
	}

	constructorArgs := constructorSpec.Args.Inputs(dag.View)
	constructorArgNames := make(map[string]struct{}, len(constructorArgs))
	for _, arg := range constructorArgs {
		constructorArgNames[arg.Name] = struct{}{}
	}

	for _, fun := range obj.TypeDef.Functions {
		modFun, err := NewModFunction(ctx, obj.Module, obj.TypeDef, fun)
		if err != nil {
			return fmt.Errorf("failed to create function %q: %w", fun.Name, err)
		}
		if err := modFun.mergeUserDefaultsTypeDefs(ctx); err != nil {
			return fmt.Errorf("failed to merge user defaults for %q: %w", fun.Name, err)
		}

		proxySpec, err := modFun.metadata.FieldSpec(ctx, obj.Module)
		if err != nil {
			return fmt.Errorf("failed to get field spec for %q: %w", fun.Name, err)
		}
		if _, exists := dag.Root().ObjectType().FieldSpec(proxySpec.Name, dag.View); exists {
			slog.ExtraDebug("skipping entrypoint proxy due to root field conflict",
				"module", obj.Module.Name(),
				"function", proxySpec.Name,
			)
			continue
		}

		methodArgs := proxySpec.Args.Inputs(dag.View)
		ambiguousArg := ""
		for _, arg := range methodArgs {
			if _, exists := constructorArgNames[arg.Name]; exists {
				ambiguousArg = arg.Name
				break
			}
		}
		if ambiguousArg != "" {
			slog.ExtraDebug("skipping entrypoint proxy due to constructor arg conflict",
				"module", obj.Module.Name(),
				"function", proxySpec.Name,
				"arg", ambiguousArg,
			)
			continue
		}

		methodSpec := proxySpec
		mergedArgs := append([]dagql.InputSpec{}, constructorArgs...)
		mergedArgs = append(mergedArgs, methodArgs...)
		proxySpec.Args = dagql.NewInputSpecs(mergedArgs...)
		proxySpec.Module = obj.Module.IDModule()
		proxySpec.GetCacheConfig = obj.entrypointProxyCacheConfig(constructorSpec, methodSpec, constructorArgs, methodArgs)
		proxySpec.NoTelemetry = true

		methodName := proxySpec.Name
		dag.Root().ObjectType().Extend(
			proxySpec,
			func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
				// Prevent dag.Select from marking the inner constructor
				// and method calls as internal — they are the real
				// user-facing calls and should appear in telemetry.
				ctx = dagql.WithNonInternalTelemetry(ctx)
				var result dagql.AnyResult
				if err := dag.Select(ctx, dag.Root(), &result,
					dagql.Selector{
						Field: constructorName,
						Args:  orderedNamedInputs(constructorArgs, args),
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
		fieldName := gqlFieldName(field.Name)
		if _, exists := dag.Root().ObjectType().FieldSpec(fieldName, dag.View); exists {
			slog.ExtraDebug("skipping entrypoint field proxy due to root field conflict",
				"module", obj.Module.Name(),
				"field", fieldName,
			)
			continue
		}

		proxySpec := dagql.FieldSpec{
			Name:        fieldName,
			Description: field.Description,
			Type:        field.TypeDef.ToTyped(),
			Module:      obj.Module.IDModule(),
			NoTelemetry: true,
		}

		// Fields have no args of their own; only constructor args are needed.
		proxySpec.Args = dagql.NewInputSpecs(constructorArgs...)
		fieldSpec := proxySpec
		proxySpec.GetCacheConfig = obj.entrypointProxyCacheConfig(constructorSpec, fieldSpec, constructorArgs, nil)

		proxiedFieldName := fieldName
		dag.Root().ObjectType().Extend(
			proxySpec,
			func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
				ctx = dagql.WithNonInternalTelemetry(ctx)
				var result dagql.AnyResult
				if err := dag.Select(ctx, dag.Root(), &result,
					dagql.Selector{
						Field: constructorName,
						Args:  orderedNamedInputs(constructorArgs, args),
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

func (obj *ModuleObject) entrypointProxyCacheConfig(
	constructorSpec dagql.FieldSpec,
	methodSpec dagql.FieldSpec,
	constructorArgs []dagql.InputSpec,
	methodArgs []dagql.InputSpec,
) dagql.GenericGetCacheConfigFunc {
	return func(
		ctx context.Context,
		parent dagql.AnyResult,
		args map[string]dagql.Input,
		view call.View,
		req dagql.GetCacheConfigRequest,
	) (*dagql.GetCacheConfigResponse, error) {
		constructorArgVals := subsetArgs(constructorArgs, args)
		constructorID := selectionIDForSpec(nil, constructorSpec, constructorArgVals, "")
		constructorReq := dagql.GetCacheConfigRequest{
			CacheKey: dagql.CacheKey{ID: constructorID},
		}
		constructorResp := &dagql.GetCacheConfigResponse{CacheKey: constructorReq.CacheKey}
		if constructorSpec.GetCacheConfig != nil {
			var err error
			constructorResp, err = constructorSpec.GetCacheConfig(
				dagql.ContextWithID(ctx, constructorID),
				parent,
				constructorArgVals,
				"",
				constructorReq,
			)
			if err != nil {
				return nil, err
			}
		}
		if constructorResp.CacheKey.ID == nil {
			return nil, fmt.Errorf("entrypoint constructor cache key is nil for %q", constructorSpec.Name)
		}

		srv := dagql.CurrentDagqlServer(ctx)
		parentObj, err := dagql.NewObjectResultForID(&ModuleObject{
			Module:  obj.Module,
			TypeDef: obj.TypeDef,
		}, srv, constructorResp.CacheKey.ID)
		if err != nil {
			return nil, err
		}

		methodArgVals := subsetArgs(methodArgs, args)
		methodReq := req
		methodReq.CacheKey.ID = selectionIDForSpec(constructorResp.CacheKey.ID, methodSpec, methodArgVals, view)

		methodResp := &dagql.GetCacheConfigResponse{CacheKey: methodReq.CacheKey}
		if methodSpec.GetCacheConfig != nil {
			methodResp, err = methodSpec.GetCacheConfig(
				dagql.ContextWithID(ctx, methodReq.CacheKey.ID),
				parentObj,
				methodArgVals,
				view,
				methodReq,
			)
			if err != nil {
				return nil, err
			}
		}

		if constructorResp.CacheKey.DoNotCache {
			methodResp.CacheKey.DoNotCache = true
		}
		if constructorResp.CacheKey.TTL != 0 && (methodResp.CacheKey.TTL == 0 || constructorResp.CacheKey.TTL < methodResp.CacheKey.TTL) {
			methodResp.CacheKey.TTL = constructorResp.CacheKey.TTL
		}

		return methodResp, nil
	}
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

func subsetArgs(specs []dagql.InputSpec, args map[string]dagql.Input) map[string]dagql.Input {
	if len(args) == 0 {
		return nil
	}

	vals := make(map[string]dagql.Input, len(specs))
	for _, spec := range specs {
		arg, ok := args[spec.Name]
		if !ok {
			continue
		}
		vals[spec.Name] = arg
	}
	if len(vals) == 0 {
		return nil
	}
	return vals
}

func selectionIDForSpec(parentID *call.ID, spec dagql.FieldSpec, args map[string]dagql.Input, view call.View) *call.ID {
	if spec.ViewFilter == nil {
		view = ""
	}

	idArgs := make([]*call.Argument, 0, len(args))
	for _, argSpec := range spec.Args.Inputs(view) {
		val, ok := args[argSpec.Name]
		if !ok {
			continue
		}
		idArgs = append(idArgs, call.NewArgument(
			argSpec.Name,
			val.ToLiteral(),
			argSpec.Sensitive,
		))
	}

	return parentID.Append(
		spec.Type.Type(),
		spec.Name,
		call.WithView(view),
		call.WithModule(spec.Module),
		call.WithArgs(idArgs...),
	)
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
	spec := &dagql.FieldSpec{
		Name:             field.Name,
		Description:      field.Description,
		Type:             field.TypeDef.ToTyped(),
		Module:           mod.IDModule(),
		GetCacheConfig:   mod.CacheConfigForCall,
		DeprecatedReason: field.Deprecated,
	}
	spec.Directives = append(spec.Directives, &ast.Directive{
		Name: trivialFieldDirectiveName,
	})
	if field.SourceMap.Valid {
		spec.Directives = append(spec.Directives, field.SourceMap.Value.TypeDirective())
	}
	return dagql.Field[*ModuleObject]{
		Spec: spec,
		Func: func(ctx context.Context, obj dagql.ObjectResult[*ModuleObject], _ map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			modType, ok, err := mod.ModTypeFor(ctx, field.TypeDef, true)
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
	}
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
func objFun(ctx context.Context, mod *Module, objDef *ObjectTypeDef, fun *Function, dag *dagql.Server) (dagql.Field[*ModuleObject], error) {
	var f dagql.Field[*ModuleObject]
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
	spec, err := fun.FieldSpec(ctx, mod)
	if err != nil {
		return f, fmt.Errorf("failed to get field spec: %w", err)
	}
	spec.Module = mod.IDModule()
	spec.GetCacheConfig = modFun.CacheConfigForCall

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

func (f *CallableField) CacheConfigForCall(
	ctx context.Context,
	parent dagql.AnyResult,
	args map[string]dagql.Input,
	view call.View,
	req dagql.GetCacheConfigRequest,
) (*dagql.GetCacheConfigResponse, error) {
	return f.Module.CacheConfigForCall(ctx, parent, args, view, req)
}
