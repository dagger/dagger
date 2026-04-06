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
	case dagql.AnyID:
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get current query: %w", err)
		}
		deps, err := query.IDDeps(ctx, x.ID())
		if err != nil {
			return nil, fmt.Errorf("failed to get deps for ID: %w", err)
		}
		dag, err := deps.Server(ctx)
		if err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		val, err := dag.Load(ctx, x.ID())
		if err != nil {
			return nil, fmt.Errorf("load ID: %w", err)
		}
		switch x := val.(type) {
		case dagql.ObjectResult[*ModuleObject]:
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

	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok {
		return fmt.Errorf("expected *ModuleObject, got %T", value)
	}
	objFields := obj.Fields

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
	if obj.isMainObject() && !opt.SkipConstructor && !opt.Entrypoint {
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
	// When the constructor function has no doc comment, fall back to the
	// object description, then the module description, so that dependency
	// constructors on Query carry a meaningful description in the shell
	// and schema — matching the no-constructor path above.
	if spec.Description == "" {
		spec.Description = formatGqlDescription(objDef.Description)
	}
	if spec.Description == "" {
		spec.Description = formatGqlDescription(mod.Description)
	}
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

	// Build constructor arg specs from the module's type definition
	// rather than looking them up from the server — the constructor
	// is not installed on the outer server when Entrypoint is set.
	var constructorArgs []dagql.InputSpec
	if obj.TypeDef.Constructor.Valid {
		fn, err := NewModFunction(ctx, obj.Module, obj.TypeDef, obj.TypeDef.Constructor.Value)
		if err != nil {
			return fmt.Errorf("failed to create constructor function: %w", err)
		}
		if err := fn.mergeUserDefaultsTypeDefs(ctx); err != nil {
			return fmt.Errorf("failed to merge constructor user defaults: %w", err)
		}
		spec, err := fn.metadata.FieldSpec(ctx, obj.Module)
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
		withDesc := obj.TypeDef.Constructor.Value.Description
		if withDesc == "" {
			withDesc = fmt.Sprintf("Configure the %s constructor arguments.", obj.Module.Name())
		}
		withSpec := dagql.FieldSpec{
			Name:        "with",
			Description: withDesc,
			Type:        &Query{},
			Module:      obj.Module.IDModule(),
			Args:        dagql.NewInputSpecs(constructorArgs...),
			DoNotCache:  "Pure routing; the inner module constructor has its own caching policy.",
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
				return dagql.NewObjectResultForCurrentID(ctx, dag, cp)
			},
		)
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
		// Proxy specs only carry the method's own args — constructor args
		// are stored on the Query via the `with` field.
		proxySpec.Module = obj.Module.IDModule()
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
		fieldName := gqlFieldName(field.Name)

		proxySpec := dagql.FieldSpec{
			Name:        fieldName,
			Description: field.Description,
			Type:        field.TypeDef.ToTyped(),
			Module:      obj.Module.IDModule(),
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
