package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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

	// Set up the node(id:) loader to resolve IDs through a server that
	// has all the module dependencies the ID requires. Without this,
	// node(id:) would try to replay the ID's call chain on the current
	// server, which may not have the necessary modules installed.
	dag.SetNodeLoader(func(ctx context.Context, id *call.ID) (dagql.AnyObjectResult, error) {
		query, err := CurrentQuery(ctx)
		if err != nil {
			// No query in context — fall back to the local server.
			return dag.Load(ctx, id)
		}
		deps, err := query.IDDeps(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("node: resolve deps: %w", err)
		}
		idServer, err := deps.Server(ctx)
		if err != nil {
			return nil, fmt.Errorf("node: build server: %w", err)
		}
		return idServer.Load(ctx, id)
	})

	dagintro.Install[*Query](dag)

	objects, ifaces, err := installModules(ctx, dag, mods)
	if err != nil {
		return nil, err
	}

	if err := registerInterfaceImpls(dag, objects, ifaces); err != nil {
		return nil, err
	}

	registerInterfaceToInterfaceImpls(dag, ifaces)

	return dag, nil
}

// installModules installs each module into the server and collects object/interface type defs.
func installModules(
	ctx context.Context,
	dag *dagql.Server,
	mods []modInstall,
) ([]*ModuleObjectType, []*InterfaceType, error) {
	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, m := range mods {
		mod := m.mod
		err := mod.Install(ctx, dag, m.opts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}

		// TODO support core interfaces types
		if userMod, ok := mod.(*Module); ok {
			defs, err := mod.TypeDefs(ctx, dag)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get type defs for module %q: %w", mod.Name(), err)
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
	return objects, ifaces, nil
}

// makeImplementsChecker returns a function that checks whether a type implements an interface,
// considering both object-implements-interface and interface-satisfies-interface relationships.
func makeImplementsChecker(
	dag *dagql.Server,
	objects []*ModuleObjectType,
	ifaces []*InterfaceType,
) func(typeName, ifaceName string) bool {
	return func(typeName, ifaceName string) bool {
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
}

// registerInterfaceImpls registers object types as implementations of interfaces they satisfy.
func registerInterfaceImpls(
	dag *dagql.Server,
	objects []*ModuleObjectType,
	ifaces []*InterfaceType,
) error {
	checker := makeImplementsChecker(dag, objects, ifaces)
	for _, objType := range objects {
		obj := objType.typeDef
		class, found := dag.ObjectType(obj.Name)
		if !found {
			return fmt.Errorf("failed to find object %q in schema", obj.Name)
		}
		for _, ifaceType := range ifaces {
			iface := ifaceType.typeDef
			if !obj.IsSubtypeOf(iface) {
				continue
			}
			dagqlIface, ok := dag.InterfaceType(iface.Name)
			if !ok {
				continue
			}
			impl, ok := class.(dagql.InterfaceImplementor)
			if !ok {
				continue
			}
			if dagqlIface.Satisfies(class, dag.View, checker) {
				impl.ImplementInterfaceUnchecked(dagqlIface)
			}
		}
	}
	return nil
}

// registerInterfaceToInterfaceImpls registers interface-implements-interface
// relationships via duck typing.
func registerInterfaceToInterfaceImpls(
	dag *dagql.Server,
	ifaces []*InterfaceType,
) {
	for _, ifaceTypeA := range ifaces {
		dagqlIfaceA, okA := dag.InterfaceType(ifaceTypeA.typeDef.Name)
		if !okA {
			continue
		}
		for _, ifaceTypeB := range ifaces {
			if ifaceTypeA.typeDef.Name == ifaceTypeB.typeDef.Name {
				continue
			}
			dagqlIfaceB, okB := dag.InterfaceType(ifaceTypeB.typeDef.Name)
			if !okB {
				continue
			}
			// Avoid circular references: don't declare A implements B if B already implements A.
			if _, alreadyReverse := dagqlIfaceB.Interfaces()[dagqlIfaceA.TypeName()]; alreadyReverse {
				continue
			}
			if dagqlIfaceB.SatisfiedByInterface(dagqlIfaceA, dag.View) {
				dagqlIfaceA.ImplementInterface(dagqlIfaceB)
			}
		}
	}
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
