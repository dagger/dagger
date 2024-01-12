package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/vektah/gqlparser/v2/ast"
)

type InterfaceType struct {
	mod *Module

	// the type def metadata, with namespacing already applied
	typeDef *InterfaceTypeDef
}

var _ ModType = (*InterfaceType)(nil)

func (iface *InterfaceType) ConvertFromSDKResult(ctx context.Context, value any) (dagql.Typed, error) {
	if value == nil {
		// TODO remove if this is OK. Why is this not handled by a wrapping Nullable instead?
		slog.Warn("InterfaceType.ConvertFromSDKResult: got nil value")
		return nil, nil
	}

	// TODO: this seems expensive
	fromID := func(id *idproto.ID) (dagql.Typed, error) {
		deps, err := iface.mod.Query.IDDeps(ctx, iface.mod.Deps.Prepend(iface.mod), id)
		if err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		dag, err := deps.Schema(ctx)
		if err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		val, err := dag.Load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load interface ID %s: %w", id.Display(), err)
		}

		typeName := val.ObjectType().TypeName()

		var checkType *TypeDef
		if objType, found, err := deps.ModTypeFor(ctx, &TypeDef{
			Kind: TypeDefKindObject,
			AsObject: dagql.NonNull(&ObjectTypeDef{
				Name: typeName,
			}),
		}); err == nil && found {
			checkType = objType.TypeDef()
		} else if ifaceType, found, err := deps.ModTypeFor(ctx, &TypeDef{
			Kind: TypeDefKindInterface,
			AsInterface: dagql.NonNull(&InterfaceTypeDef{
				Name: typeName,
			}),
		}); err == nil && found {
			checkType = ifaceType.TypeDef()
		} else {
			return nil, fmt.Errorf("could not find object or interface type for %q", typeName)
		}

		// Verify that the object provided actually implements the interface. This
		// is also enforced by only adding "As*" fields to objects in a schema once
		// they implement the interface, but in theory an SDK could provide
		// arbitrary IDs of objects here, so we need to check again to be fully
		// robust.
		if ok := checkType.IsSubtypeOf(iface.TypeDef()); !ok {
			return nil, fmt.Errorf("type %s does not implement interface %s", typeName, iface.typeDef.Name)
		}
		return val, nil
	}

	switch value := value.(type) {
	case string:
		var id idproto.ID
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

// nolint:gocyclo
func (iface *InterfaceType) Install(ctx context.Context, dag *dagql.Server) error {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("interface", iface.typeDef.Name))
	bklog.G(ctx).Debug("installing interface")

	if iface.mod.InstanceID == nil {
		return fmt.Errorf("installing interface %q too early", iface.typeDef.Name)
	}
	class := dagql.NewClass(dagql.ClassOpts[*InterfaceAnnotatedValue]{
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
		fnTypeDef := fnTypeDef
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

		fieldDef := dagql.FieldSpec{
			Name:        fnName,
			Description: formatGqlDescription(fnTypeDef.Description),
			Type:        fnTypeDef.ReturnType.ToTyped(),
			Module:      iface.mod.InstanceID,
		}

		argTypeDefsByName := map[string]*TypeDef{}
		for _, argMetadata := range fnTypeDef.Args {
			argMetadata := argMetadata
			argTypeDefsByName[argMetadata.Name] = argMetadata.TypeDef

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
				Name:        gqlArgName(argMetadata.Name),
				Description: formatGqlDescription(argMetadata.Description),
				Type:        argMetadata.TypeDef.ToInput(),
			}
			fieldDef.Args = append(fieldDef.Args, inputSpec)
		}

		fields = append(fields, dagql.Field[*InterfaceAnnotatedValue]{
			Spec: fieldDef,
			Func: func(ctx context.Context, self dagql.Instance[*InterfaceAnnotatedValue], args map[string]dagql.Input) (dagql.Typed, error) {
				runtimeVal := self.Self

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

				res, err := callable.Call(ctx, dagql.CurrentID(ctx), &CallOpts{
					Inputs:    callInputs,
					ParentVal: runtimeVal.Fields,
				})
				if err != nil {
					return nil, fmt.Errorf("failed to call interface function %s.%s: %w", ifaceName, fieldDef.Name, err)
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
				return wrapIface(ctx, dag, ifaceReturnType, objReturnType, res)
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
			Pure:        false, // no need to cache this; what if the ID is impure?
			Args: []dagql.InputSpec{
				{
					Name: "id",
					Type: idScalar,
				},
			},
			Module: iface.mod.InstanceID,
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			return iface.ConvertFromSDKResult(ctx, args["id"])
		},
	)

	return nil
}

func wrapIface(ctx context.Context, dag *dagql.Server, ifaceType *InterfaceType, underlyingType ModType, res dagql.Typed) (dagql.Typed, error) {
	switch underlyingType := underlyingType.(type) {
	case *InterfaceType, *ModuleObjectType:
		switch res := res.(type) {
		case *ModuleObject:
			return &InterfaceAnnotatedValue{
				TypeDef:        ifaceType.typeDef,
				IfaceType:      ifaceType,
				Fields:         res.Fields,
				UnderlyingType: underlyingType,
			}, nil
		case dagql.Instance[*InterfaceAnnotatedValue]:
			return res, nil
		default:
			return nil, fmt.Errorf("unexpected object return type %T for %s", res, ifaceType.typeDef.Name)
		}
	case *ListType:
		if res == nil {
			slog.Debug("wrapIface got nil list return") // TODO remove log once confirmed needed
			return res, nil
		}
		enum, ok := res.(dagql.Enumerable)
		if !ok {
			return nil, fmt.Errorf("expected Enumerable return, got %T", res)
		}
		if enum.Len() == 0 {
			return res, nil
		}
		ret := dagql.DynamicArrayOutput{}
		for i := 1; i <= enum.Len(); i++ {
			item, err := enum.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get item %d: %w", i, err)
			}
			if ret.Elem == nil { // set the return type
				ret.Elem = item
			}
			val, err := wrapIface(ctx, dag, ifaceType, underlyingType.Underlying, item)
			if err != nil {
				return nil, fmt.Errorf("failed to wrap item %d: %w", i, err)
			}
			ret.Values = append(ret.Values, val)
		}
		return ret, nil
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

var _ HasPBDefinitions = (*InterfaceAnnotatedValue)(nil)

func (iface *InterfaceAnnotatedValue) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	defs := []*pb.Definition{}
	objDef := iface.UnderlyingType.TypeDef().AsObject.Value
	for name, val := range iface.Fields {
		fieldDef, ok := objDef.FieldByOriginalName(name)
		if !ok {
			// TODO: private field; skip. (this is a bug)
			continue
		}
		fieldType, ok, err := iface.UnderlyingType.SourceMod().ModTypeFor(ctx, fieldDef.TypeDef, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get mod type for field %q: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find mod type for field %q", name)
		}
		converted, err := fieldType.ConvertFromSDKResult(ctx, val)
		if err != nil {
			return nil, fmt.Errorf("failed to convert arg %q: %w", name, err)
		}
		fieldDefs, err := collectPBDefinitions(ctx, converted)
		if err != nil {
			return nil, err
		}
		defs = append(defs, fieldDefs...)
	}
	return defs, nil
}
