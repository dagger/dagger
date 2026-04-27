package core

import (
	"context"
	"fmt"
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

func (iface *InterfaceType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	idable, ok := value.(dagql.IDable)
	if !ok {
		return nil, fmt.Errorf("unexpected interface value type for conversion to sdk input %T", value)
	}
	id, err := idable.ID()
	if err != nil {
		return nil, fmt.Errorf("get interface ID: %w", err)
	}
	if id == nil {
		return nil, nil
	}
	return id.Encode()
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

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (iface *InterfaceType) Install(ctx context.Context, dag *dagql.Server) error {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("interface", iface.typeDef.Name))
	slog.ExtraDebug("installing interface")

	if iface.mod.Self() == nil {
		return fmt.Errorf("installing interface %q too early", iface.typeDef.Name)
	}

	ifaceTypeDef := iface.typeDef
	ifaceName := gqlObjectName(ifaceTypeDef.Name)
	moduleID, err := NewUserMod(iface.mod).ResultCallModule(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve module identity for interface %q: %w", ifaceName, err)
	}
	ifaceMod := iface.SourceMod()

	// Create a dagql.Interface instead of a Class — this is the first-class
	// interface representation that produces ast.Interface definitions.
	dagqlIface := dagql.NewInterface(ifaceName, formatGqlDescription(ifaceTypeDef.Description))

	// Add an "id" field so SDKs can generate proper ID handling methods
	// (e.g. XXX_GraphQLType, MarshalJSON) for interface types.
	dagqlIface.AddField(dagql.InterfaceFieldSpec{
		FieldSpec: dagql.FieldSpec{
			Name:        "id",
			Description: fmt.Sprintf("A unique identifier for this %s.", ifaceName),
			Type:        dagql.AnyID{},
		},
	})

	installDirectives := []*ast.Directive{}
	if iface.typeDef.SourceMap.Valid {
		installDirectives = append(installDirectives, iface.typeDef.SourceMap.Value.Self().TypeDirective())
	}

	// Add field specs from the interface's function definitions.
	// We keep the validation that return/arg types aren't from external deps.
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

		fieldSpec := dagql.InterfaceFieldSpec{
			FieldSpec: dagql.FieldSpec{
				Name:             fnName,
				Description:      formatGqlDescription(fnTypeDef.Description),
				Type:             fnTypeDef.ReturnType.Self().ToTyped(),
				Module:           moduleID,
				DeprecatedReason: fnTypeDef.Deprecated,
			},
		}
		if fnTypeDef.SourceMap.Valid {
			fieldSpec.Directives = append(fieldSpec.Directives, fnTypeDef.SourceMap.Value.Self().TypeDirective())
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
			// Add @expectedType directive for ID-typed arguments.
			// Walk through list wrappers to find the underlying type.
			expectedTypeDef := argMetadata.TypeDef.Self()
			for expectedTypeDef.Kind == TypeDefKindList && expectedTypeDef.AsList.Valid {
				expectedTypeDef = expectedTypeDef.AsList.Value.Self().ElementTypeDef.Self()
			}
			if expectedTypeDef.Kind == TypeDefKindObject && expectedTypeDef.AsObject.Valid {
				inputSpec.Directives = append(inputSpec.Directives, dagql.ExpectedTypeDirective(expectedTypeDef.AsObject.Value.Self().Name))
			} else if expectedTypeDef.Kind == TypeDefKindInterface && expectedTypeDef.AsInterface.Valid {
				inputSpec.Directives = append(inputSpec.Directives, dagql.ExpectedTypeDirective(expectedTypeDef.AsInterface.Value.Self().Name))
			}
			if argMetadata.SourceMap.Valid {
				inputSpec.Directives = append(inputSpec.Directives, argMetadata.SourceMap.Value.Self().TypeDirective())
			}
			fieldSpec.Args.Add(inputSpec)
		}

		dagqlIface.AddField(fieldSpec)
	}

	// Install the interface into the dagql server schema.
	dag.InstallInterface(dagqlIface, installDirectives...)

	return nil
}

// interfaceTypedMarker is a Typed marker that returns an interface type name.
// Used by typedef.go to express interface return types in module function specs.
type interfaceTypedMarker struct {
	name string
}

var _ dagql.Typed = (*interfaceTypedMarker)(nil)

func (m *interfaceTypedMarker) Type() *ast.Type {
	return &ast.Type{
		NamedType: m.name,
		NonNull:   true,
	}
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
