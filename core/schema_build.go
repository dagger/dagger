package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	dagintro "github.com/dagger/dagger/dagql/introspection"
)

type modInstall struct {
	mod  Mod
	opts InstallOpts
}

func buildSchema(
	ctx context.Context,
	root *Query,
	mods []modInstall,
) (*dagql.Server, error) {
	var coreMod coreSchemaForker
	for _, mod := range mods {
		if m, ok := mod.mod.(coreSchemaForker); ok {
			coreMod = m
			break
		}
	}

	var view call.View
	for _, mod := range mods {
		if version, ok := mod.mod.View(); ok {
			view = version
			break
		}
	}

	var dag *dagql.Server
	if coreMod != nil {
		forked, err := coreMod.ForkSchema(ctx, root, view)
		if err != nil {
			return nil, fmt.Errorf("failed to fork core schema base: %w", err)
		}
		dag = forked
	} else {
		var err error
		dag, err = dagql.NewServer(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("create schema server: %w", err)
		}
		dag.View = view
		dag.Around(AroundFunc)
		dagintro.Install[*Query](dag)
	}

	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, mod := range mods {
		if _, ok := mod.mod.(coreSchemaForker); ok && coreMod != nil {
			continue
		}
		if err := mod.mod.Install(ctx, dag, mod.opts); err != nil {
			return nil, fmt.Errorf("failed to get schema for module %q: %w", mod.mod.Name(), err)
		}

		userMod, ok := mod.mod.(*userMod)
		if !ok {
			continue
		}
		defs, err := mod.mod.TypeDefs(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("failed to get type defs for module %q: %w", mod.mod.Name(), err)
		}
		for _, def := range defs {
			switch def.Self().Kind {
			case TypeDefKindObject:
				objects = append(objects, &ModuleObjectType{
					typeDef: def.Self().AsObject.Value.Self(),
					mod:     userMod.res,
				})
			case TypeDefKindInterface:
				ifaces = append(ifaces, &InterfaceType{
					typeDef: def.Self().AsInterface.Value.Self(),
					mod:     userMod.res,
				})
			}
		}
	}

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
			ifaceModule, err := NewUserMod(ifaceType.mod).ResultCallModule(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve module identity for interface %q: %w", iface.Name, err)
			}
			asIfaceFieldName := gqlFieldName(fmt.Sprintf("as%s", iface.Name))
			class.Extend(
				dagql.FieldSpec{
					Name:        asIfaceFieldName,
					Description: fmt.Sprintf("Converts this %s to a %s.", obj.Name, iface.Name),
					Type:        &InterfaceAnnotatedValue{TypeDef: iface},
					Module:      ifaceModule,
				},
				func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
					inst, ok := dagql.UnwrapAs[*ModuleObject](self)
					if !ok {
						return nil, fmt.Errorf("expected %T to be a ModuleObject", self)
					}
					return dagql.NewObjectResultForCurrentCall(ctx, dag, &InterfaceAnnotatedValue{
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
