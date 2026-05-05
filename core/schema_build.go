package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	dagintro "github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
)

type modInstall struct {
	mod  Mod
	opts InstallOpts
}

// InstallCoreSchemaLoaders configures DagQL loaders that need Dagger's module
// dependency model. Core schema forks are also used directly by client sessions,
// so this is installed outside buildSchema as well as inside module-aware schema
// builders.
func InstallCoreSchemaLoaders(dag *dagql.Server) {
	serverForResultCall := func(ctx context.Context, resultCall *dagql.ResultCall) (*dagql.Server, error) {
		query, ok := currentQuery(ctx)
		if !ok {
			return dag, nil
		}
		deps, err := query.ModDepsForCall(ctx, resultCall)
		if err != nil {
			return nil, err
		}
		resultServer, err := deps.Schema(ctx)
		if err != nil {
			return nil, err
		}
		return resultServer, nil
	}

	// Set up cache-result and node(id:) loaders to resolve cached objects through
	// a server that has all module dependencies required by the result call. Without
	// this, reconstructing a cached dependency-module object would be limited to the
	// current server's schema, which may only contain core types.
	dag.SetResultServerForCall(serverForResultCall)
	dag.SetNodeLoader(func(ctx context.Context, id *call.ID) (dagql.AnyObjectResult, error) {
		if id == nil || id.EngineResultID() == 0 {
			return dag.Load(ctx, id)
		}
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("node: current client metadata: %w", err)
		}
		if clientMetadata.SessionID == "" {
			return nil, fmt.Errorf("node: empty session ID")
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, fmt.Errorf("node: engine cache: %w", err)
		}
		resultCall, err := cache.ResultCallByResultID(ctx, clientMetadata.SessionID, id.EngineResultID())
		if err != nil {
			return nil, fmt.Errorf("node: load result call: %w", err)
		}
		idServer, err := serverForResultCall(ctx, resultCall)
		if err != nil {
			return nil, fmt.Errorf("node: resolve deps: %w", err)
		}
		return idServer.Load(ctx, id)
	})
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

	InstallCoreSchemaLoaders(dag)

	objects, ifaces, err := installModules(ctx, dag, mods, coreMod)
	if err != nil {
		return nil, err
	}

	if err := registerInterfaceImpls(dag, objects, ifaces); err != nil {
		return nil, err
	}

	registerInterfaceToInterfaceImpls(dag, objects, ifaces)

	return dag, nil
}

// installModules installs each module into the server and collects object/interface type defs.
func installModules(
	ctx context.Context,
	dag *dagql.Server,
	mods []modInstall,
	coreMod coreSchemaForker,
) ([]*ModuleObjectType, []*InterfaceType, error) {
	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, mod := range mods {
		if _, ok := mod.mod.(coreSchemaForker); ok && coreMod != nil {
			continue
		}
		if err := mod.mod.Install(ctx, dag, mod.opts); err != nil {
			return nil, nil, fmt.Errorf("failed to get schema for module %q: %w", mod.mod.Name(), err)
		}

		userMod, ok := mod.mod.(*userMod)
		if !ok {
			continue
		}
		defs, err := mod.mod.TypeDefs(ctx, dag)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get type defs for module %q: %w", mod.mod.Name(), err)
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
			return targetIface.SatisfiedByInterface(typeIface, dag.View)
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
		class, found := dag.ObjectType(objType.typeDef.Name)
		if !found {
			return fmt.Errorf("failed to find object %q in schema", objType.typeDef.Name)
		}
		for _, ifaceType := range ifaces {
			dagqlIface, ok := dag.InterfaceType(ifaceType.typeDef.Name)
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
	objects []*ModuleObjectType,
	ifaces []*InterfaceType,
) {
	checker := makeImplementsChecker(dag, objects, ifaces)
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
			if dagqlIfaceB.SatisfiedByInterface(dagqlIfaceA, dag.View, checker) {
				dagqlIfaceA.ImplementInterface(dagqlIfaceB)
			}
		}
	}
}

func schemaJSONFileFromServer(ctx context.Context, dag *dagql.Server, hiddenTypes []string) (dagql.Result[*File], error) {
	var schemaJSONFile dagql.Result[*File]
	if err := dag.Select(ctx, dag.Root(), &schemaJSONFile,
		dagql.Selector{
			Field: "__schemaJSONFile",
			// Programmatic selectors do not inherit the server view, but this file's
			// contents include __schemaVersion and must match the module's view.
			View: dag.View,
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
