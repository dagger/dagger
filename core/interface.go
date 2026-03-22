package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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
	dag, err := deps.Server(ctx)
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

func (iface *InterfaceType) CollectContent(ctx context.Context, value dagql.AnyResult, content *CollectedContent) error {
	if value == nil {
		return content.CollectJSONable(nil)
	}

	// Load the implementation via its ID to get the concrete type and
	// delegate content collection to it.
	loadedImpl, err := iface.loadImpl(ctx, value.ID())
	if err != nil {
		return fmt.Errorf("load interface implementation: %w", err)
	}

	return loadedImpl.valType.CollectContent(ctx, loadedImpl.val, content)
}

func (iface *InterfaceType) ConvertToSDKInput(ctx context.Context, value dagql.Typed) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch value := value.(type) {
	case dagql.AnyID:
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

	ifaceTypeDef := iface.typeDef
	ifaceName := gqlObjectName(ifaceTypeDef.Name)

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
		installDirectives = append(installDirectives, iface.typeDef.SourceMap.Value.TypeDirective())
	}

	// Add field specs from the interface's function definitions.
	// We keep the validation that return/arg types aren't from external deps.
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

		fieldSpec := dagql.InterfaceFieldSpec{
			FieldSpec: dagql.FieldSpec{
				Name:             fnName,
				Description:      formatGqlDescription(fnTypeDef.Description),
				Type:             fnTypeDef.ReturnType.ToTyped(),
				Module:           iface.mod.IDModule(),
				DeprecatedReason: fnTypeDef.Deprecated,
			},
		}
		if fnTypeDef.SourceMap.Valid {
			fieldSpec.Directives = append(fieldSpec.Directives, fnTypeDef.SourceMap.Value.TypeDirective())
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
			// Add @expectedType directive for ID-typed arguments.
			if argMetadata.TypeDef.Kind == TypeDefKindObject && argMetadata.TypeDef.AsObject.Valid {
				inputSpec.Directives = append(inputSpec.Directives, dagql.ExpectedTypeDirective(argMetadata.TypeDef.AsObject.Value.Name))
			} else if argMetadata.TypeDef.Kind == TypeDefKindInterface && argMetadata.TypeDef.AsInterface.Valid {
				inputSpec.Directives = append(inputSpec.Directives, dagql.ExpectedTypeDirective(argMetadata.TypeDef.AsInterface.Value.Name))
			}
			if argMetadata.SourceMap.Valid {
				inputSpec.Directives = append(inputSpec.Directives, argMetadata.SourceMap.Value.TypeDirective())
			}
			fieldSpec.Args.Add(inputSpec)
		}

		dagqlIface.AddField(fieldSpec)
	}

	// Install the interface into the dagql server schema.
	dag.InstallInterface(dagqlIface, installDirectives...)

	// Use a thin Typed marker for the return type of loadFooFromID.
	// It just returns the interface name as the GraphQL type.
	ifaceTyped := &interfaceTypedMarker{name: ifaceName}

	// Install loadFooFromID to allow loading any ID that implements this interface.
	// Use generic ID type with @expectedType directive.
	dag.Root().ObjectType().Extend(
		dagql.FieldSpec{
			Name:        fmt.Sprintf("load%sFromID", ifaceName),
			Description: fmt.Sprintf("Load a %s from its ID.", ifaceName),
			Type:        ifaceTyped,
			Args: dagql.NewInputSpecs(
				dagql.InputSpec{
					Name:       "id",
					Type:       dagql.AnyID{},
					Directives: []*ast.Directive{dagql.ExpectedTypeDirective(ifaceName)},
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

// interfaceTypedMarker is a thin Typed wrapper used as the return type for
// loadFooFromID fields. It just returns the interface name as the GraphQL type.
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
