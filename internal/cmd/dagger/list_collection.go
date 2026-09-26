package daggercmd

import (
	"context"
	"slices"
	"strings"

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
	id, err := dag.CurrentWorkspace().Artifacts().ID(ctx)
	if err != nil {
		return nil, err
	}
	all := dagger.Ref[*dagger.Artifacts](dag, id)
	definitions, err := artifactDimensions(ctx, dag, all)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for id, allocated := range artifactDimensionFlagNames(cmd, definitions) {
		names[id] = allocated.Key
	}
	paths, err := readArtifactListPaths(ctx, dag, all)
	if err != nil {
		return nil, err
	}
	index, err := newArtifactNameIndex(paths)
	if err != nil {
		return nil, err
	}
	if format == "cli" {
		types := commandArtifactTypes(cmd)
		if typ := cmd.Annotations[artifactListType]; typ != "" {
			types = []string{typ}
		}
		omitArtifactCLITypeKeys(items, index, types)
	}

	moduleFlags := artifactModuleFlagNames(cmd, definitions, paths)
	shortNames := map[string]string{}
	for i := range items {
		item := &items[i]
		filters := itemDimensionFilters(*item)
		item.DisplayKeys = map[string]string{}
		for _, key := range item.DimensionKeys {
			if key.Dimension == artifact.ModuleDimension {
				item.ModuleFlag = moduleFlags[key.Key]
			}
			if strings.HasPrefix(key.Dimension, "type:") {
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

func itemDimensionFilters(item listedArtifact) []dagaddress.Pair {
	var filters []dagaddress.Pair
	for _, key := range item.DimensionKeys {
		filters = append(filters, dagaddress.Pair{Dimension: key.Dimension, Key: key.Key, HasKey: true})
	}
	return filters
}
