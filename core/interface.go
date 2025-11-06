package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

type InterfaceType struct {
	mod *Module

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
		checkType := loadedImpl.valType.TypeDef()

		// Verify that the object provided actually implements the interface. This
		// is also enforced by only adding "As*" fields to objects in a schema once
		// they implement the interface, but in theory an SDK could provide
		// arbitrary IDs of objects here, so we need to check again to be fully
		// robust.
		if ok := checkType.IsSubtypeOf(iface.TypeDef()); !ok {
			return nil, fmt.Errorf("type %s does not implement interface %s", typeName, iface.typeDef.Name)
		}

		return loadedImpl.val, nil
	}

	switch value := value.(type) {
	case string:
		var id call.ID
		if err := id.Decode(value); err != nil {
			return nil, fmt.Errorf("decode ID: %w", err)
		}
		return fromID(&id)
	case dagql.IDable:
		return fromID(value.ID())
	default:
		return nil, fmt.Errorf("unexpected interface value type for conversion from sdk result %T: %+v", value, value)
	}
}

func (iface *InterfaceType) loadImpl(ctx context.Context, id *call.ID) (*loadedIfaceImpl, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("current query: %w", err)
	}
	deps, err := query.IDDeps(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	dag, err := deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	val, err := dag.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load interface ID %s: %w", id.DisplaySelf(), err)
	}

	typeName := val.ObjectType().TypeName()

	var modType ModType
	var found bool

	// try first as an object, then as an interface
	modType, found, err = deps.ModTypeFor(ctx, &TypeDef{
		Kind: TypeDefKindObject,
		AsObject: dagql.NonNull(&ObjectTypeDef{
			Name: typeName,
		}),
	})
	if err != nil || !found {
		modType, found, err = deps.ModTypeFor(ctx, &TypeDef{
			Kind: TypeDefKindInterface,
			AsInterface: dagql.NonNull(&InterfaceTypeDef{
				Name: typeName,
			}),
		})
	}
	if err != nil || !found {
		return nil, fmt.Errorf("could not find object or interface type for %q", typeName)
	}

	loadedImpl := &loadedIfaceImpl{
		val:     val,
		valType: modType,
	}
	return loadedImpl, nil
}

func (iface *InterfaceType) CollectCoreIDs(ctx context.Context, value dagql.AnyResult, ids map[digest.Digest]*resource.ID) error {
	if value == nil {
		return nil
	}

	switch innerVal := value.Unwrap().(type) {
	case *InterfaceAnnotatedValue:
		mod, ok := innerVal.UnderlyingType.SourceMod().(*Module)
		if !ok {
			return fmt.Errorf("unexpected source mod type %T", innerVal.UnderlyingType.SourceMod())
		}

		obj, err := dagql.NewResultForID(&ModuleObject{
			Module:  mod,
			TypeDef: innerVal.UnderlyingType.TypeDef().AsObject.Value,
			Fields:  innerVal.Fields,
		}, value.ID())
		if err != nil {
			return fmt.Errorf("create module object from interface value: %w", err)
		}

		return innerVal.UnderlyingType.CollectCoreIDs(ctx, obj, ids)

	case *ModuleObject:
		loadedImpl, err := iface.loadImpl(ctx, value.ID())
		if err != nil {
			return fmt.Errorf("load interface implementation: %w", err)
		}

		return loadedImpl.valType.CollectCoreIDs(ctx, loadedImpl.val, ids)

	case nil:
		return nil

	default:
		return fmt.Errorf("unexpected interface value type for collecting IDs %T", value)
	}
}

func (iface *InterfaceType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch value := value.(type) {
	case DynamicID:
		return value.ID().Encode()
	default:
		return nil, fmt.Errorf("unexpected interface value type for conversion to sdk input %T", value)
	}
}

func (iface *InterfaceType) SourceMod() Mod {
	return iface.mod
}

func (iface *InterfaceType) TypeDef() *TypeDef {
	return &TypeDef{
		Kind:        TypeDefKindInterface,
		AsInterface: dagql.NonNull(iface.typeDef.Clone()),
	}
}

func (iface *InterfaceType) Install(ctx context.Context, dag *dagql.Server) error {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("interface", iface.typeDef.Name))
	slog.ExtraDebug("installing interface")

	if iface.mod.ResultID == nil {
		return fmt.Errorf("installing interface %q too early", iface.typeDef.Name)
	}
	class := dagql.NewClass(dag, dagql.ClassOpts[*InterfaceAnnotatedValue]{
		Typed: &InterfaceAnnotatedValue{
			TypeDef:   iface.typeDef,
			IfaceType: iface,
		},
	})

	dag.InstallObject(class)

	ifaceTypeDef := iface.typeDef
	ifaceName := gqlObjectName(ifaceTypeDef.Name)

	fields := make([]dagql.Field[*InterfaceAnnotatedValue], 0, len(iface.typeDef.Functions))
	for _, fnTypeDef := range iface.typeDef.Functions {
		fnName := gqlFieldName(fnTypeDef.Name)

		// check whether this is a pre-existing object from a dependency module
		returnModType, ok, err := iface.mod.Deps.ModTypeFor(ctx, fnTypeDef.ReturnType)
		if err != nil {
			return fmt.Errorf("failed to get mod type for type def: %w", err)
		}
		if ok {
			// can either be a core type or a type from *this* module
			switch {
			case returnModType.SourceMod() == nil:
			case returnModType.SourceMod().Name() == ModuleName:
			case returnModType.SourceMod() == iface.mod:
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
			Type:             fnTypeDef.ReturnType.ToTyped(),
			Module:           iface.mod.IDModule(),
			DeprecatedReason: fnTypeDef.Deprecated,
		}
		if fnTypeDef.SourceMap.Valid {
			fieldDef.Directives = append(fieldDef.Directives, fnTypeDef.SourceMap.Value.TypeDirective())
		}

		for _, argMetadata := range fnTypeDef.Args {
			// check whether this is a pre-existing object from a dependency module
			argModType, ok, err := iface.mod.Deps.ModTypeFor(ctx, argMetadata.TypeDef)
			if err != nil {
				return fmt.Errorf("failed to get mod type for type def: %w", err)
			}
			if ok {
				// can either be a core type or a type from *this* module
				switch {
				case argModType.SourceMod() == nil:
				case argModType.SourceMod().Name() == ModuleName:
				case argModType.SourceMod() == iface.mod:
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
				Type:             argMetadata.TypeDef.ToInput(),
				DeprecatedReason: argMetadata.Deprecated,
			}
			if argMetadata.SourceMap.Valid {
				inputSpec.Directives = append(inputSpec.Directives, argMetadata.SourceMap.Value.TypeDirective())
			}
			fieldDef.Args.Add(inputSpec)
		}

		fieldDef.GetCacheConfig = func(
			ctx context.Context,
			parentObj dagql.AnyResult,
			args map[string]dagql.Input,
			view call.View,
			req dagql.GetCacheConfigRequest,
		) (*dagql.GetCacheConfigResponse, error) {
			parent, ok := parentObj.(dagql.ObjectResult[*InterfaceAnnotatedValue])
			if !ok {
				return nil, fmt.Errorf("unexpected parent object type %T", parentObj)
			}
			runtimeVal := parent.Self()

			// TODO: support core types too
			userModObj, ok := runtimeVal.UnderlyingType.(*ModuleObjectType)
			if !ok {
				return nil, fmt.Errorf("unexpected underlying type %T for interface resolver %s.%s", runtimeVal.UnderlyingType, ifaceName, fieldDef.Name)
			}

			callable, err := userModObj.GetCallable(ctx, fieldDef.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get callable for %s.%s: %w", ifaceName, fieldDef.Name, err)
			}

			return callable.CacheConfigForCall(ctx, parentObj, args, view, req)
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

				postCall := res.GetPostCall()
				if postCall != nil {
					if err := postCall(ctx); err != nil {
						return nil, fmt.Errorf("failed to run post-call for %s.%s: %w", ifaceName, fieldDef.Name, err)
					}
				}

				if fnTypeDef.ReturnType.Underlying().Kind != TypeDefKindInterface {
					return res, nil
				}

				// if the return type of this function is an interface or list of interface, we may need to wrap the
				// return value of the underlying object's function (due to support for covariant matching on return types)

				underlyingReturnType, ok, err := iface.mod.ModTypeFor(ctx, fnTypeDef.ReturnType.Underlying(), true)
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
				return wrapIface(dagql.CurrentID(ctx), ifaceReturnType, objReturnType, res, dag)
			},
		})
	}

	class.Install(fields...)
	dag.InstallObject(class)

	idScalar := DynamicID{
		typeName: iface.typeDef.Name,
	}

	// override loadFooFromID to allow any ID that implements this interface
	dag.Root().ObjectType().Extend(
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
			Module:     iface.mod.IDModule(),
			DoNotCache: "There's no point caching the loading call of an ID vs. letting the ID's calls cache on their own.",
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			return iface.ConvertFromSDKResult(ctx, args["id"])
		},
	)

	return nil
}

func wrapIface(curID *call.ID, ifaceType *InterfaceType, underlyingType ModType, res dagql.AnyResult, srv *dagql.Server) (dagql.AnyResult, error) {
	switch underlyingType := underlyingType.(type) {
	case *InterfaceType, *ModuleObjectType:
		switch wrappedRes := res.Unwrap().(type) {
		case *ModuleObject:
			return dagql.NewObjectResultForID(&InterfaceAnnotatedValue{
				TypeDef:        ifaceType.typeDef,
				IfaceType:      ifaceType,
				Fields:         wrappedRes.Fields,
				UnderlyingType: underlyingType,
			}, srv, curID)

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
			item, err := res.NthValue(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get item %d: %w", i, err)
			}
			if ret.Elem == nil { // set the return type
				ret.Elem = item.Unwrap()
			}
			nthID := curID.SelectNth(i)
			val, err := wrapIface(nthID, ifaceType, underlyingType.Underlying, item, srv)
			if err != nil {
				return nil, fmt.Errorf("failed to wrap item %d: %w", i, err)
			}
			ret.Values = append(ret.Values, val)
		}
		return dagql.NewResultForID(&ret, curID)

	default:
		return res, nil
	}
}

type InterfaceAnnotatedValue struct {
	TypeDef        *InterfaceTypeDef
	IfaceType      *InterfaceType
	Fields         map[string]any
	UnderlyingType ModType
}

var _ dagql.InterfaceValue = (*InterfaceAnnotatedValue)(nil)

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
		def.Directives = append(def.Directives, iface.TypeDef.SourceMap.Value.TypeDirective())
	}
	return def
}

var _ HasPBDefinitions = (*InterfaceAnnotatedValue)(nil)

func (iface *InterfaceAnnotatedValue) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	defs := []*pb.Definition{}
	objDef := iface.UnderlyingType.TypeDef().AsObject.Value
	for _, field := range objDef.Fields {
		// TODO: we skip over private fields, we can't convert them anyways (this is a bug)
		name := field.OriginalName
		val, ok := iface.Fields[name]
		if !ok {
			// missing field
			continue
		}
		fieldType, ok, err := iface.UnderlyingType.SourceMod().ModTypeFor(ctx, field.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for field %q: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for field %q", name)
		}

		curID := dagql.CurrentID(ctx)
		fieldID := curID.Append(
			field.TypeDef.ToType(),
			field.Name,
			call.WithView(curID.View()),
			call.WithModule(curID.Module()),
		)
		ctx := dagql.ContextWithID(ctx, fieldID)

		converted, err := fieldType.ConvertFromSDKResult(ctx, val)
		if err != nil {
			return nil, fmt.Errorf("failed to convert arg %q: %w", name, err)
		}
		fieldDefs, err := collectPBDefinitions(ctx, converted.Unwrap())
		if err != nil {
			return nil, err
		}
		defs = append(defs, fieldDefs...)
	}
	return defs, nil
}
