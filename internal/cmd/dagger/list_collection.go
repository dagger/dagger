package daggercmd

import (
	"context"
	"slices"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
)

func prepareArtifactOutput(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, items []listedArtifact) (map[string]string, error) {
	format, _ := cmd.Flags().GetString("format")
	if len(items) == 0 || format == "link" {
		return nil, nil
	}
	// All naming queries must reuse this discovery result. currentWorkspace
	// creates a new workspace on each call.
	workspaceID, err := core.NewQuery(dag).CurrentWorkspace().ID(ctx)
	if err != nil {
		return nil, err
	}
	ws := core.Ref[*core.Workspace](core.NewQuery(dag), workspaceID)
	include, err := artifactOutputModules(ctx, ws, items)
	if err != nil {
		return nil, err
	}
	id, err := ws.Artifacts(core.WorkspaceArtifactsOpts{Include: include}).ID(ctx)
	if err != nil {
		return nil, err
	}
	all := core.Ref[*core.Artifacts](core.NewQuery(dag), id)
	definitions, err := artifactDimensions(ctx, dag, all)
	if err != nil {
		return nil, err
	}
	paths, err := readArtifactListPaths(ctx, dag, all)
	if err != nil {
		return nil, err
	}
	return prepareArtifactOutputWithSchema(cmd, items, definitions, paths)
}

func prepareArtifactOutputWithSchema(cmd *cobra.Command, items []listedArtifact, definitions artifact.Dimensions, paths []artifactListPath) (map[string]string, error) {
	format, _ := cmd.Flags().GetString("format")
	if len(items) == 0 || format == "link" {
		return nil, nil
	}
	names := map[string]string{}
	presence := map[string]string{}
	for id, allocated := range artifactDimensionFlagNames(cmd, definitions) {
		names[id], presence[id] = allocated.Key, allocated.Presence
	}
	if format != "cli" {
		for _, dimension := range definitions {
			if dimension.Kind == "COLLECTION" && dimension.Identifier == dimension.CollectionType {
				names[dimension.Identifier] = dimension.Name
			}
		}
	}
	index, err := newArtifactNameIndex(paths)
	if err != nil {
		return nil, err
	}
	if err := omitCollectionTypeKeys(items, paths); err != nil {
		return nil, err
	}
	if format == "cli" {
		types := artifactCLIOutputTypes(cmd, definitions)
		omitArtifactCLITypeKeys(items, index, types)
		if err := prepareCanonicalArtifactCLIPaths(items, paths, types); err != nil {
			return nil, err
		}
	}

	moduleFlags := artifactModuleFlagNames(cmd, definitions, paths)
	shortNames := map[string]string{}
	for i := range items {
		item := &items[i]
		filters := itemDimensionFilters(*item)
		item.DisplayKeys, item.PresenceNames = map[string]string{}, presence
		for _, key := range item.DimensionKeys {
			if key.Dimension == artifact.ModuleDimension {
				item.ModuleFlag = moduleFlags[key.Key]
			}
			if item.Coordinates == nil && strings.HasPrefix(key.Dimension, "type:") {
				var ids []string
				for _, filter := range filters {
					ids = append(ids, filter.Dimension)
				}
				slices.Sort(ids)
				cacheKey := key.Dimension + "\x00" + key.Key + "\x00" + strings.Join(slices.Compact(ids), "\x00")
				name, ok := shortNames[cacheKey]
				if !ok {
					name = index.short(key.Dimension, key.Key, filters)
					shortNames[cacheKey] = name
				}
				item.DisplayKeys[key.Dimension] = name
			}
		}
	}
	return names, nil
}

// artifactOutputModules returns the modules whose artifacts name the items'
// flags: the items' modules, and the modules that can take a name from them.
// It returns nil, for every module, when an item has no module.
func artifactOutputModules(ctx context.Context, ws *core.Workspace, items []listedArtifact) ([]string, error) {
	var modules []string
	for _, item := range items {
		i := slices.IndexFunc(item.DimensionKeys, func(key struct{ Dimension, Key string }) bool {
			return key.Dimension == artifact.ModuleDimension
		})
		if i < 0 {
			return nil, nil
		}
		modules = append(modules, item.DimensionKeys[i].Key)
	}
	all, err := workspaceModuleNames(ctx, ws)
	if err != nil {
		return nil, err
	}
	slices.Sort(modules)
	modules = slices.Compact(modules)
	if slices.ContainsFunc(modules, func(module string) bool { return !slices.Contains(all, module) }) {
		return nil, nil
	}
	return artifactNamingModules(modules, all), nil
}

func itemDimensionFilters(item listedArtifact) []dagaddress.Pair {
	var filters []dagaddress.Pair
	for _, key := range item.DimensionKeys {
		filters = append(filters, dagaddress.Pair{Dimension: key.Dimension, Key: key.Key, HasKey: true})
	}
	for _, id := range item.Presence {
		filters = append(filters, dagaddress.Pair{Dimension: id})
	}
	return filters
}

func artifactCLIOutputTypes(cmd *cobra.Command, definitions artifact.Dimensions) []string {
	if typ := cmd.Annotations[artifactListType]; typ != "" {
		return []string{typ}
	}
	if collection := cmd.Annotations[artifactListCollection]; collection != "" {
		var types []string
		for _, definition := range definitions {
			if definition.CollectionType == collection && definition.ItemType != "" && !slices.Contains(types, definition.ItemType) {
				types = append(types, definition.ItemType)
			}
		}
		return types
	}
	return commandArtifactTypes(cmd)
}
