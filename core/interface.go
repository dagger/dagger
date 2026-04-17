package core

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

type InterfaceType struct {
	mod dagql.ObjectResult[*Module]

	// the type def metadata, with namespacing already applied
	typeDef *InterfaceTypeDef
}

var _ ModType = (*InterfaceType)(nil)

type loadedIfaceImpl struct {
	val     dagql.AnyObjectResult
	valType ModType
}

var _ ModType = (*InterfaceType)(nil)

func (iface *InterfaceType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.AnyResult, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("InterfaceType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	// TODO: this seems expensive
	fromID := func(id *call.ID) (dagql.AnyObjectResult, error) {
		loadedImpl, err := iface.loadImpl(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load interface implementation: %w", err)
		}
		typeName := loadedImpl.val.Type().Name()
		checkTypeRes, err := loadedImpl.valType.TypeDef(ctx)
		if err != nil {
			return nil, fmt.Errorf("load interface implementation typedef: %w", err)
		}
		checkType := checkTypeRes.Self()

		// Verify that the object provided actually implements the interface. This
		// is also enforced by only adding "As*" fields to objects in a schema once
		// they implement the interface, but in theory an SDK could provide
		// arbitrary IDs of objects here, so we need to check again to be fully
		// robust.
		ifaceTypeRes, err := iface.TypeDef(ctx)
		if err != nil {
			return nil, fmt.Errorf("interface typedef: %w", err)
		}
		if ok := checkType.IsSubtypeOf(ifaceTypeRes.Self()); !ok {
			return nil, fmt.Errorf("type %s does not implement interface %s", typeName, iface.typeDef.Name)
		}

		return loadedImpl.val, nil
	}

	switch value := value.(type) {
	case dagql.AnyObjectResult:
		typeName := value.Type().Name()
		loadedImpl := &loadedIfaceImpl{val: value}
		var err error
		objTypeDef, err := SelectTypeDef(ctx, dagql.Selector{
			Field: "withObject",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(typeName)}},
		})
		if err != nil {
			return nil, fmt.Errorf("resolve interface implementation object type def: %w", err)
		}
		loadedImpl.valType, _, err = iface.mod.Self().Deps.ModTypeFor(ctx, objTypeDef.Self())
		if err != nil {
			return nil, fmt.Errorf("resolve interface implementation type: %w", err)
		}
		if loadedImpl.valType == nil {
			ifaceTypeDef, err := SelectTypeDef(ctx, dagql.Selector{
				Field: "withInterface",
				Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(typeName)}},
			})
			if err != nil {
				return nil, fmt.Errorf("resolve interface implementation interface type def: %w", err)
			}
			loadedImpl.valType, _, err = iface.mod.Self().Deps.ModTypeFor(ctx, ifaceTypeDef.Self())
			if err != nil {
				return nil, fmt.Errorf("resolve interface implementation type: %w", err)
			}
		}
		if loadedImpl.valType == nil {
			return nil, fmt.Errorf("could not find object or interface type for %q", typeName)
		}
		loadedImplTypeRes, err := loadedImpl.valType.TypeDef(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve interface implementation typedef: %w", err)
		}
		ifaceTypeRes, err := iface.TypeDef(ctx)
		if err != nil {
			return nil, fmt.Errorf("interface typedef: %w", err)
		}
		if ok := loadedImplTypeRes.Self().IsSubtypeOf(ifaceTypeRes.Self()); !ok {
			return nil, fmt.Errorf("type %s does not implement interface %s", typeName, iface.typeDef.Name)
		}
		return value, nil
	case string:
		var id call.ID
		if err := id.Decode(value); err != nil {
			return nil, fmt.Errorf("decode ID: %w", err)
		}
		return fromID(&id)
	case dagql.IDable:
		id, err := value.ID()
		if err != nil {
			return nil, fmt.Errorf("get interface ID: %w", err)
		}
		return fromID(id)
	default:
		return nil, fmt.Errorf("unexpected interface value type for conversion from sdk result %T: %+v", value, value)
	}
}

func (iface *InterfaceType) loadImpl(ctx context.Context, id *call.ID) (*loadedIfaceImpl, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("current query: %w", err)
	}
	if id == nil || id.EngineResultID() == 0 {
		return nil, fmt.Errorf("load interface implementation: expected attached result ID")
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("current client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("load interface implementation: empty session ID")
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine cache: %w", err)
	}
	call, err := cache.ResultCallByResultID(ctx, clientMetadata.SessionID, id.EngineResultID())
	if err != nil {
		return nil, fmt.Errorf("load interface implementation call: %w", err)
	}
	deps, err := query.ModDepsForCall(ctx, call)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	dag, err := deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("load dependency schema: %w", err)
	}
	objVal, err := dag.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load interface implementation: %w", err)
	}

	typeName := objVal.ObjectType().TypeName()

	var modType ModType
	var found bool
	objTypeDef, err := SelectTypeDef(ctx, dagql.Selector{
		Field: "withObject",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(typeName)}},
	})
	if err != nil {
		return nil, fmt.Errorf("build object type def for %q: %w", typeName, err)
	}

	// try first as an object, then as an interface
	modType, found, err = deps.ModTypeFor(ctx, objTypeDef.Self())
	if err != nil || !found {
		ifaceTypeDef, ifaceErr := SelectTypeDef(ctx, dagql.Selector{
			Field: "withInterface",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(typeName)}},
		})
		if ifaceErr != nil {
			return nil, fmt.Errorf("build interface type def for %q: %w", typeName, ifaceErr)
		}
		modType, found, err = deps.ModTypeFor(ctx, ifaceTypeDef.Self())
	}
	if err != nil || !found {
		return nil, fmt.Errorf("could not find object or interface type for %q", typeName)
	}

	loadedImpl := &loadedIfaceImpl{
		val:     objVal,
		valType: modType,
	}
	return loadedImpl, nil
}

func (iface *InterfaceType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}

	if interfaceValue, ok := dagql.UnwrapAs[*InterfaceAnnotatedValue](value); ok {
		modInst := interfaceValue.UnderlyingType.SourceMod().ModuleResult()
		if modInst.Self() == nil {
			return fmt.Errorf("unexpected source mod type %T", interfaceValue.UnderlyingType.SourceMod())
		}
		id, err := value.ID()
		if err != nil {
			return fmt.Errorf("resolve interface value raw id: %w", err)
		}
		if id == nil {
			return fmt.Errorf("resolve interface value raw id: nil")
		}

		call, err := value.ResultCall()
		if err != nil {
			return fmt.Errorf("resolve interface value call: %w", err)
		}

		obj, err := dagql.NewResultForCall(&ModuleObject{
			Module: modInst,
			TypeDef: func() *ObjectTypeDef {
				typeDef, err := interfaceValue.UnderlyingType.TypeDef(ctx)
				if err != nil || typeDef.Self().AsObject.Value.Self() == nil {
					return nil
				}
				return typeDef.Self().AsObject.Value.Self()
			}(),
			Fields: interfaceValue.Fields,
		}, call)
		if err != nil {
			return fmt.Errorf("create module object from interface value: %w", err)
		}
		if obj.Self().TypeDef == nil {
			typeDef, err := interfaceValue.UnderlyingType.TypeDef(ctx)
			if err != nil {
				return fmt.Errorf("resolve interface underlying typedef: %w", err)
			}
			if typeDef.Self().AsObject.Value.Self() == nil {
				return fmt.Errorf("expected object typedef for interface underlying type, got %s", typeDef.Self().Kind)
			}
		}

		return interfaceValue.UnderlyingType.CollectContent(ctx, obj, content)
	}

	if _, ok := dagql.UnwrapAs[*ModuleObject](value); ok {
		id, err := value.ID()
		if err != nil {
			return fmt.Errorf("resolve interface implementation raw id: %w", err)
		}
		if id == nil {
			return fmt.Errorf("resolve interface implementation raw id: nil")
		}
		loadedImpl, err := iface.loadImpl(ctx, id)
		if err != nil {
			return fmt.Errorf("load interface implementation: %w", err)
		}

		return loadedImpl.valType.CollectContent(ctx, loadedImpl.val, content)
	}

	return fmt.Errorf("expected *InterfaceAnnotatedValue, *ModuleObject, or nil, got %T (%s)", value, value.Type())
}

func (iface *InterfaceType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch value := value.(type) {
	case dagql.AnyObjectResult:
		id, err := value.ID()
		if err != nil {
			return nil, fmt.Errorf("get interface object ID: %w", err)
		}
		if id == nil {
			return nil, nil
		}
		return id.Encode()
	case DynamicID:
		id, err := value.ID()
		if err != nil {
			return nil, fmt.Errorf("get dynamic interface ID: %w", err)
		}
		return id.Encode()
	default:
		return nil, fmt.Errorf("unexpected interface value type for conversion to sdk input %T", value)
	}
}

func (iface *InterfaceType) SourceMod() Mod {
	if iface.mod.Self() == nil {
		return nil
	}
	return NewUserMod(iface.mod)
}

func (iface *InterfaceType) TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error) {
	var sourceMap dagql.Optional[dagql.ID[*SourceMap]]
	var err error
	if iface.typeDef.SourceMap.Valid {
		sourceMap, err = OptionalResultIDInput(iface.typeDef.SourceMap.Value)
		if err != nil {
			return dagql.ObjectResult[*TypeDef]{}, err
		}
	}
	return SelectTypeDef(ctx, dagql.Selector{
		Field: "withInterface",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(iface.typeDef.Name)},
			{Name: "description", Value: dagql.String(iface.typeDef.Description)},
			{Name: "sourceMap", Value: sourceMap},
			{Name: "sourceModuleName", Value: OptSourceModuleName(iface.typeDef.SourceModuleName)},
		},
	})
}

func (iface *InterfaceType) Install(ctx context.Context, dag *dagql.Server) error {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("interface", iface.typeDef.Name))
	slog.ExtraDebug("installing interface")

	if iface.mod.Self() == nil {
		return fmt.Errorf("installing interface %q too early", iface.typeDef.Name)
	}

	classOpts := dagql.ClassOpts[*InterfaceAnnotatedValue]{
		Typed: &InterfaceAnnotatedValue{
			TypeDef:   iface.typeDef,
			IfaceType: iface,
		},
	}

	installDirectives := []*ast.Directive{}
	if iface.typeDef.SourceMap.Valid {
		classOpts.SourceMap = iface.typeDef.SourceMap.Value.Self().TypeDirective()
		installDirectives = append(installDirectives, iface.typeDef.SourceMap.Value.Self().TypeDirective())
	}

	class := dagql.NewClass(dag, classOpts)
	dag.InstallObject(class, installDirectives...)

	ifaceTypeDef := iface.typeDef
	ifaceName := gqlObjectName(ifaceTypeDef.Name)
	moduleID, err := NewUserMod(iface.mod).ResultCallModule(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve module identity for interface %q: %w", ifaceName, err)
	}
	ifaceMod := iface.SourceMod()

	fields := make([]dagql.Field[*InterfaceAnnotatedValue], 0, len(iface.typeDef.Functions))
	for _, fnTypeDefRes := range iface.typeDef.Functions {
		fnTypeDef := fnTypeDefRes.Self()
		fnName := gqlFieldName(fnTypeDef.Name)

		// check whether this is a pre-existing object from a dependency module
		returnModType, ok, err := iface.mod.Self().Deps.ModTypeFor(ctx, fnTypeDef.ReturnType.Self())
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			sameSourceMod := false
			if sourceMod := returnModType.SourceMod(); sourceMod != nil && ifaceMod != nil {
				sameSourceMod, err = sourceMod.Same(ifaceMod)
				if err != nil {
					return fmt.Errorf("compare return type source module for interface %q function %q: %w", ifaceName, fnName, err)
				}
			}
			// can either be a core type or a type from *this* module
			switch {
			case returnModType.SourceMod() == nil:
			case returnModType.SourceMod().Name() == ModuleName:
			case sameSourceMod:
			default:
				return fmt.Errorf("interface %q function %q cannot return external type from dependency module %q",
					ifaceName,
					fnName,
					returnModType.SourceMod().Name(),
				)
			}
		}

		fieldDef := &dagql.FieldSpec{
			Name:             fnName,
			Description:      formatGqlDescription(fnTypeDef.Description),
			Type:             fnTypeDef.ReturnType.Self().ToTyped(),
			Module:           moduleID,
			DeprecatedReason: fnTypeDef.Deprecated,
		}
		if fnTypeDef.SourceMap.Valid {
			fieldDef.Directives = append(fieldDef.Directives, fnTypeDef.SourceMap.Value.Self().TypeDirective())
		}

		for _, argMetadataRes := range fnTypeDef.Args {
			argMetadata := argMetadataRes.Self()
			// check whether this is a pre-existing object from a dependency module
			argModType, ok, err := iface.mod.Self().Deps.ModTypeFor(ctx, argMetadata.TypeDef.Self())
			if err != nil {
				return fmt.Errorf("failed to get mod type for type def: %w", err)
			}
			if ok {
				sameSourceMod := false
				if sourceMod := argModType.SourceMod(); sourceMod != nil && ifaceMod != nil {
					sameSourceMod, err = sourceMod.Same(ifaceMod)
					if err != nil {
						return fmt.Errorf("compare arg type source module for interface %q function %q arg %q: %w", ifaceName, fnName, argMetadata.Name, err)
					}
				}
				// can either be a core type or a type from *this* module
				switch {
				case argModType.SourceMod() == nil:
				case argModType.SourceMod().Name() == ModuleName:
				case sameSourceMod:
				default:
					return fmt.Errorf("interface %q function %q cannot accept arg %q of external type from dependency module %q",
						ifaceName,
						fnName,
						argMetadata.Name,
						argModType.SourceMod().Name(),
					)
				}
			}

			inputSpec := dagql.InputSpec{
				Name:             gqlArgName(argMetadata.Name),
				Description:      formatGqlDescription(argMetadata.Description),
				Type:             argMetadata.TypeDef.Self().ToInput(),
				DeprecatedReason: argMetadata.Deprecated,
			}
			if argMetadata.SourceMap.Valid {
				inputSpec.Directives = append(inputSpec.Directives, argMetadata.SourceMap.Value.Self().TypeDirective())
			}
			fieldDef.Args.Add(inputSpec)
		}

		fieldDef.GetDynamicInput = func(
			ctx context.Context,
			parentObj dagql.AnyResult,
			args map[string]dagql.Input,
			view call.View,
			req *dagql.CallRequest,
		) error {
			parent, ok := parentObj.(dagql.ObjectResult[*InterfaceAnnotatedValue])
			if !ok {
				return fmt.Errorf("unexpected parent object type %T", parentObj)
			}
			runtimeVal := parent.Self()

			// TODO: support core types too
			userModObj, ok := runtimeVal.UnderlyingType.(*ModuleObjectType)
			if !ok {
				return fmt.Errorf("unexpected underlying type %T for interface resolver %s.%s", runtimeVal.UnderlyingType, ifaceName, fieldDef.Name)
			}

			callable, err := userModObj.GetCallable(ctx, fieldDef.Name)
			if err != nil {
				return fmt.Errorf("failed to get callable for %s.%s: %w", ifaceName, fieldDef.Name, err)
			}

			return callable.DynamicInputsForCall(ctx, parentObj, args, view, req)
		}

		fields = append(fields, dagql.Field[*InterfaceAnnotatedValue]{
			Spec: fieldDef,
			Func: func(ctx context.Context, self dagql.ObjectResult[*InterfaceAnnotatedValue], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				runtimeVal := self.Self()

				// TODO: support core types too
				userModObj, ok := runtimeVal.UnderlyingType.(*ModuleObjectType)
				if !ok {
					return nil, fmt.Errorf("unexpected underlying type %T for interface resolver %s.%s", runtimeVal.UnderlyingType, ifaceName, fieldDef.Name)
				}

				callable, err := userModObj.GetCallable(ctx, fieldDef.Name)
				if err != nil {
					return nil, fmt.Errorf("failed to get callable for %s.%s: %w", ifaceName, fieldDef.Name, err)
				}

				callInputs := make([]CallInput, 0, len(args))
				for k, argVal := range args {
					callInputs = append(callInputs, CallInput{
						Name:  k,
						Value: argVal,
					})
				}

				res, err := callable.Call(ctx, &CallOpts{
					Inputs:       callInputs,
					ParentTyped:  self,
					ParentFields: runtimeVal.Fields,
					Server:       dag,
				})
				if err != nil {
					return nil, fmt.Errorf("failed to call interface function %s.%s: %w", ifaceName, fieldDef.Name, err)
				}

				if fnTypeDef.ReturnType.Self().Underlying().Kind != TypeDefKindInterface {
					return res, nil
				}

				// if the return type of this function is an interface or list of interface, we may need to wrap the
				// return value of the underlying object's function (due to support for covariant matching on return types)

				underlyingReturnType, ok, err := ifaceMod.ModTypeFor(ctx, fnTypeDef.ReturnType.Self().Underlying(), true)
				if err != nil {
					return nil, fmt.Errorf("failed to get return mod type: %w", err)
				}
				if !ok {
					return nil, fmt.Errorf("failed to find return mod type")
				}
				ifaceReturnType, ok := underlyingReturnType.(*InterfaceType)
				if !ok {
					return nil, fmt.Errorf("expected return interface type, got %T", underlyingReturnType)
				}
				objReturnType, err := callable.ReturnType()
				if err != nil {
					return nil, fmt.Errorf("failed to get object return type for %s.%s: %w", ifaceName, fieldDef.Name, err)
				}
				return wrapIface(ctx, dagql.CurrentCall(ctx), ifaceReturnType, objReturnType, res, dag)
			},
		})
	}

	class.Install(fields...)
	dag.InstallObject(class, installDirectives...)

	idScalar := DynamicID{
		typeName: iface.typeDef.Name,
	}

	// override loadFooFromID to allow any ID that implements this interface
	dag.Root().ObjectType().ExtendLoadByID(
		dagql.FieldSpec{
			Name:        fmt.Sprintf("load%sFromID", class.TypeName()),
			Description: fmt.Sprintf("Load a %s from its ID.", class.TypeName()),
			Type:        class.Typed(),
			Args: dagql.NewInputSpecs(
				dagql.InputSpec{
					Name: "id",
					Type: idScalar,
				},
			),
			Module: moduleID,
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			idable, ok := args["id"].(dagql.IDable)
			if !ok {
				return nil, fmt.Errorf("expected IDable, got %T", args["id"])
			}
			id, err := idable.ID()
			if err != nil {
				return nil, fmt.Errorf("get interface load ID: %w", err)
			}
			if id == nil {
				return nil, fmt.Errorf("expected non-nil ID")
			}
			loadedImpl, err := iface.loadImpl(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("load interface implementation: %w", err)
			}
			typeName := loadedImpl.val.Type().Name()
			loadedImplType, err := loadedImpl.valType.TypeDef(ctx)
			if err != nil {
				return nil, fmt.Errorf("resolve loaded implementation typedef: %w", err)
			}
			ifaceType, err := iface.TypeDef(ctx)
			if err != nil {
				return nil, fmt.Errorf("resolve interface typedef: %w", err)
			}
			if ok := loadedImplType.Self().IsSubtypeOf(ifaceType.Self()); !ok {
				return nil, fmt.Errorf("type %s does not implement interface %s", typeName, iface.typeDef.Name)
			}
			return wrapIface(ctx, nil, iface, loadedImpl.valType, loadedImpl.val, dag)
		},
	)

	return nil
}

func wrapIface(
	ctx context.Context,
	callFrame *dagql.ResultCall,
	ifaceType *InterfaceType,
	underlyingType ModType,
	res dagql.AnyResult,
	srv *dagql.Server,
) (dagql.AnyResult, error) {
	switch underlyingType := underlyingType.(type) {
	case *InterfaceType, *ModuleObjectType:
		switch wrappedRes := res.Unwrap().(type) {
		case *ModuleObject:
			call, err := wrappedIfaceCall(callFrame, res)
			if err != nil {
				return nil, fmt.Errorf("resolve interface wrapper call: %w", err)
			}
			return dagql.NewObjectResultForCall(&InterfaceAnnotatedValue{
				TypeDef:        ifaceType.typeDef,
				IfaceType:      ifaceType,
				Fields:         wrappedRes.Fields,
				UnderlyingType: underlyingType,
			}, srv, call)

		case *InterfaceAnnotatedValue:
			return res, nil

		default:
			return nil, fmt.Errorf("unexpected return type %T for interface %s", wrappedRes, ifaceType.typeDef.Name)
		}

	case *ListType:
		if res == nil {
			return res, nil
		}
		enum, ok := res.Unwrap().(dagql.Enumerable)
		if !ok {
			return nil, fmt.Errorf("expected Enumerable return, got %T", res)
		}
		if enum.Len() == 0 {
			return res, nil
		}

		ret := dagql.DynamicResultArrayOutput{}
		for i := 1; i <= enum.Len(); i++ {
			item, err := res.NthValue(ctx, i)
			if err != nil {
				return nil, fmt.Errorf("failed to get item %d: %w", i, err)
			}
			if ret.Elem == nil { // set the return type
				ret.Elem = item.Unwrap()
			}
			val, err := wrapIface(ctx, wrappedIfaceNthCall(callFrame, i), ifaceType, underlyingType.Underlying, item, srv)
			if err != nil {
				return nil, fmt.Errorf("failed to wrap item %d: %w", i, err)
			}
			ret.Values = append(ret.Values, val)
		}
		call, err := wrappedIfaceCall(callFrame, res)
		if err != nil {
			return nil, fmt.Errorf("resolve interface list wrapper call: %w", err)
		}
		return dagql.NewResultForCall(&ret, call)

	default:
		return res, nil
	}
}

func wrappedIfaceCall(callFrame *dagql.ResultCall, res dagql.AnyResult) (*dagql.ResultCall, error) {
	if callFrame != nil {
		return cloneResultCall(callFrame), nil
	}
	return res.ResultCall()
}

func wrappedIfaceNthCall(callFrame *dagql.ResultCall, nth int) *dagql.ResultCall {
	if callFrame == nil {
		return nil
	}
	cp := cloneResultCall(callFrame)
	cp.Nth = int64(nth)
	if cp.Type != nil {
		cp.Type = cloneResultCallType(cp.Type.Elem)
	}
	return cp
}

func cloneResultCall(call *dagql.ResultCall) *dagql.ResultCall {
	if call == nil {
		return nil
	}
	cp := &dagql.ResultCall{
		Kind:        call.Kind,
		Type:        cloneResultCallType(call.Type),
		Field:       call.Field,
		SyntheticOp: call.SyntheticOp,
		View:        call.View,
		Nth:         call.Nth,
		Receiver:    call.Receiver,
		Module:      call.Module,
	}
	if call.EffectIDs != nil {
		cp.EffectIDs = slices.Clone(call.EffectIDs)
	}
	if call.ExtraDigests != nil {
		cp.ExtraDigests = slices.Clone(call.ExtraDigests)
	}
	if call.Args != nil {
		cp.Args = slices.Clone(call.Args)
	}
	if call.ImplicitInputs != nil {
		cp.ImplicitInputs = slices.Clone(call.ImplicitInputs)
	}
	return cp
}

func cloneResultCallType(typ *dagql.ResultCallType) *dagql.ResultCallType {
	if typ == nil {
		return nil
	}
	return &dagql.ResultCallType{
		NamedType: typ.NamedType,
		NonNull:   typ.NonNull,
		Elem:      cloneResultCallType(typ.Elem),
	}
}

type InterfaceAnnotatedValue struct {
	TypeDef        *InterfaceTypeDef
	IfaceType      *InterfaceType
	Fields         map[string]any
	UnderlyingType ModType
}

var _ dagql.InterfaceValue = (*InterfaceAnnotatedValue)(nil)
var _ dagql.HasDependencyResults = (*InterfaceAnnotatedValue)(nil)

func (iface *InterfaceAnnotatedValue) UnderlyingObject() (dagql.Typed, error) {
	userModObjType, ok := iface.UnderlyingType.(*ModuleObjectType)
	if !ok {
		return nil, fmt.Errorf("unhandled underlying type %T for interface value %s", iface.UnderlyingType, iface.TypeDef.Name)
	}
	return &ModuleObject{
		Module:  userModObjType.mod,
		TypeDef: userModObjType.typeDef,
		Fields:  iface.Fields,
	}, nil
}

func (iface *InterfaceAnnotatedValue) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if iface == nil || len(iface.Fields) == 0 {
		return nil, nil
	}
	owned := make([]dagql.AnyResult, 0)
	for _, name := range slices.Sorted(maps.Keys(iface.Fields)) {
		updated, deps, err := attachModuleObjectValue(attach, iface.Fields[name])
		if err != nil {
			return nil, fmt.Errorf("attach interface field %q: %w", name, err)
		}
		iface.Fields[name] = updated
		owned = append(owned, deps...)
	}
	return owned, nil
}

var _ dagql.Typed = (*InterfaceAnnotatedValue)(nil)

func (iface *InterfaceAnnotatedValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: iface.TypeDef.Name,
		NonNull:   true,
	}
}

func (iface *InterfaceAnnotatedValue) TypeDescription() string {
	return iface.TypeDef.Description
}

func (iface *InterfaceAnnotatedValue) TypeDefinition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Object,
		Name: iface.Type().Name(),
	}
	if iface.TypeDef.SourceMap.Valid {
		def.Directives = append(def.Directives, iface.TypeDef.SourceMap.Value.Self().TypeDirective())
	}
	return def
}
