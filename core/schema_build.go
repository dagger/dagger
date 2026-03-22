package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	dagintro "github.com/dagger/dagger/dagql/introspection"
)

// modInstall pairs a module with its install options for schema building.
type modInstall struct {
	mod  Mod
	opts InstallOpts
}

// buildSchema creates a dagql server with the given modules installed and
// wires up interface extensions.
func buildSchema(
	ctx context.Context,
	root *Query,
	mods []modInstall,
) (*dagql.Server, error) {
	dagqlCache, err := root.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache: %w", err)
	}
	dag := dagql.NewServer(root, dagqlCache)
	for _, m := range mods {
		if version, ok := m.mod.View(); ok {
			dag.View = version
			break
		}
	}

	dag.Around(AroundFunc)

	dagintro.Install[*Query](dag)

	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, mod := range d.Mods {
		err := mod.Install(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}

		// TODO support core interfaces types
		if userMod, ok := mod.(*Module); ok {
			defs, err := mod.TypeDefs(ctx, dag)
			if err != nil {
				return nil, fmt.Errorf("failed to get type defs for module %q: %w", mod.Name(), err)
			}
			for _, def := range defs {
				switch def.Kind {
				case TypeDefKindObject:
					objects = append(objects, &ModuleObjectType{
						typeDef: def.AsObject.Value,
						mod:     userMod,
					})
				case TypeDefKindInterface:
					ifaces = append(ifaces, &InterfaceType{
						typeDef: def.AsInterface.Value,
						mod:     userMod,
					})
				}
			}
		}
	}

	// Register interface implementations and add deprecated asFoo fields.
	for _, objType := range objects {
		obj := objType.typeDef
		class, found := dag.ObjectType(obj.Name)
		if !found {
			return nil, fmt.Errorf("failed to find object %q in schema", obj.Name)
		}
		for _, ifaceType := range ifaces {
			iface := ifaceType.typeDef
			if !obj.IsSubtypeOf(iface) {
				continue
			}

			// Register first-class interface implementation in dagql.
			// Only declare the relationship when the object's fields
			// strictly match the interface's field types (name + exact type).
			// Core's IsSubtypeOf allows covariant return types, but
			// GraphQL schema validation requires exact matches, so we
			// use dagql's Satisfies check for the schema declaration.
			dagqlIface, ok := dag.InterfaceType(iface.Name)
			if ok {
				if impl, ok := class.(dagql.InterfaceImplementor); ok {
					if dagqlIface.Satisfies(class, dag.View) {
						impl.ImplementInterfaceUnchecked(dagqlIface)
					}
				}
			}

			// Add deprecated asFoo field for backward compatibility.
			// These will be removed in a future release; clients should
			// query interface fields directly on the concrete object.
			asIfaceFieldName := gqlFieldName(fmt.Sprintf("as%s", iface.Name))
			deprecatedReason := fmt.Sprintf(
				"Use %s directly instead of converting via %s.",
				obj.Name, asIfaceFieldName,
			)
			class.Extend(
				dagql.FieldSpec{
					Name:             asIfaceFieldName,
					Description:      fmt.Sprintf("Converts this %s to a %s.", obj.Name, iface.Name),
					Type:             &InterfaceAnnotatedValue{TypeDef: iface},
					Module:           ifaceType.mod.IDModule(),
					GetCacheConfig:   ifaceType.mod.CacheConfigForCall,
					DeprecatedReason: &deprecatedReason,
				},
				func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
					inst, ok := dagql.UnwrapAs[*ModuleObject](self)
					if !ok {
						return nil, fmt.Errorf("expected %T to be a ModuleObject", self)
					}
					// Return an InterfaceAnnotatedValue wrapping the concrete object.
					// toSelectable handles the InterfaceValue unwrapping to reach
					// the concrete class for field resolution.
					return dagql.NewResultForCurrentID(ctx, &InterfaceAnnotatedValue{
						TypeDef:        iface,
						Fields:         inst.Fields,
						UnderlyingType: objType,
						IfaceType:      ifaceType,
					})
				},
			)
		}
	}

	return dag, nil
}

// schemaJSONFileFromServer generates an introspection JSON file from an
// already-built dagql server, optionally hiding the given types.
func schemaJSONFileFromServer(ctx context.Context, dag *dagql.Server, hiddenTypes []string) (dagql.Result[*File], error) {
	var schemaJSONFile dagql.Result[*File]
	if err := dag.Select(ctx, dag.Root(), &schemaJSONFile,
		dagql.Selector{
			Field: "__schemaJSONFile",
			Args: []dagql.NamedInput{
				{
					Name:  "hiddenTypes",
					Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(hiddenTypes...)),
				},
			},
		},
	); err != nil {
		return schemaJSONFile, fmt.Errorf("failed to select introspection JSON file: %w", err)
	}
	return schemaJSONFile, nil
}
