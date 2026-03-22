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
	for _, m := range mods {
		mod := m.mod
		err := mod.Install(ctx, dag, m.opts)
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

	// Register interface implementations.
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

			dagqlIface, ok := dag.InterfaceType(iface.Name)
			if ok {
				if impl, ok := class.(dagql.InterfaceImplementor); ok {
					checker := func(typeName, ifaceName string) bool {
						// Check if typeName (an object) implements ifaceName (an interface).
						for _, ot := range objects {
							if ot.typeDef.Name == typeName {
								for _, it := range ifaces {
									if it.typeDef.Name == ifaceName {
										return ot.typeDef.IsSubtypeOf(it.typeDef)
									}
								}
							}
						}
						// Check if typeName is an interface that structurally satisfies ifaceName.
						// This handles cases like ImplLocalOtherIface satisfying TestOtherIface.
						typeIface, typeOK := dag.InterfaceType(typeName)
						targetIface, targetOK := dag.InterfaceType(ifaceName)
						if typeOK && targetOK {
							// Check that all fields in targetIface exist in typeIface
							// with compatible types (recursive checker).
							for _, targetField := range targetIface.FieldSpecs(dag.View) {
								typeField, ok := typeIface.FieldSpec(targetField.Name, dag.View)
								if !ok {
									return false
								}
								// Simple name match for now (exact types).
								if targetField.Type.Type().Name() != typeField.Type.Type().Name() {
									return false
								}
							}
							return true
						}
						return false
					}
					if dagqlIface.Satisfies(class, dag.View, checker) {
						impl.ImplementInterfaceUnchecked(dagqlIface)
					}
				}
			}
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
