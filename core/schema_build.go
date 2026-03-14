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

// buildSchema creates a dagql server with the given modules installed, wires
// up interface extensions, and produces the introspection JSON file.
func buildSchema(
	ctx context.Context,
	root *Query,
	mods []modInstall,
	hiddenTypes []string,
) (
	*dagql.Server,
	dagql.Result[*File],
	error,
) {
	var schemaJSONFile dagql.Result[*File]

	dagqlCache, err := root.Cache(ctx)
	if err != nil {
		return nil, schemaJSONFile, fmt.Errorf("failed to get cache: %w", err)
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
	for _, m := range mods {
		if err := m.mod.Install(ctx, dag, m.opts); err != nil {
			return nil, schemaJSONFile, fmt.Errorf("failed to get schema for module %q: %w", m.mod.Name(), err)
		}

		// TODO support core interfaces types
		if userMod, ok := m.mod.(*Module); ok {
			defs, err := m.mod.TypeDefs(ctx, dag)
			if err != nil {
				return nil, schemaJSONFile, fmt.Errorf("failed to get type defs for module %q: %w", m.mod.Name(), err)
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

	// Wire up interface extensions: for each object that implements an
	// interface, add an asXxx conversion field.
	for _, objType := range objects {
		obj := objType.typeDef
		class, found := dag.ObjectType(obj.Name)
		if !found {
			return nil, schemaJSONFile, fmt.Errorf("failed to find object %q in schema", obj.Name)
		}
		for _, ifaceType := range ifaces {
			iface := ifaceType.typeDef
			if !obj.IsSubtypeOf(iface) {
				continue
			}
			asIfaceFieldName := gqlFieldName(fmt.Sprintf("as%s", iface.Name))
			class.Extend(
				dagql.FieldSpec{
					Name:           asIfaceFieldName,
					Description:    fmt.Sprintf("Converts this %s to a %s.", obj.Name, iface.Name),
					Type:           &InterfaceAnnotatedValue{TypeDef: iface},
					Module:         ifaceType.mod.IDModule(),
					GetCacheConfig: ifaceType.mod.CacheConfigForCall,
				},
				func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
					inst, ok := dagql.UnwrapAs[*ModuleObject](self)
					if !ok {
						return nil, fmt.Errorf("expected %T to be a ModuleObject", self)
					}
					return dagql.NewObjectResultForCurrentID(ctx, dag, &InterfaceAnnotatedValue{
						TypeDef:        iface,
						Fields:         inst.Fields,
						UnderlyingType: objType,
						IfaceType:      ifaceType,
					})
				},
			)
		}
	}

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
		return nil, schemaJSONFile, fmt.Errorf("failed to select introspection JSON file: %w", err)
	}

	return dag, schemaJSONFile, nil
}
