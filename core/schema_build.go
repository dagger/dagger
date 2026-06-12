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
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
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

	// Fallback resolver for cache reconstruction and persisted-envelope decoding
	// when the current schema does not have the referenced object type
	// installed. Cache hits normally short-circuit via the class captured on
	// the shared result, so this hook only fires on the cold path (decode, or
	// loads that bypassed class capture).
	dag.SetResultServerForCall(serverForResultCall)
	dag.SetNodeLoader(func(ctx context.Context, id *call.ID) (dagql.AnyObjectResult, error) {
		if id == nil || !id.IsHandle() || id.EngineResultID() == 0 {
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

	if err := installModules(ctx, dag, mods, coreMod); err != nil {
		return nil, err
	}

	return dag, nil
}

// installModules installs each module into the server.
func installModules(
	ctx context.Context,
	dag *dagql.Server,
	mods []modInstall,
	coreMod coreSchemaForker,
) error {
	for _, mod := range mods {
		if _, ok := mod.mod.(coreSchemaForker); ok && coreMod != nil {
			continue
		}
		if err := mod.mod.Install(ctx, dag, mod.opts); err != nil {
			return fmt.Errorf("failed to get schema for module %q: %w", mod.mod.Name(), err)
		}
	}
	return nil
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
