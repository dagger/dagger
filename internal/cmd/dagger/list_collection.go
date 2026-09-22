package daggercmd

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
)

// Collection CLI rows are reusable filters. Check names and paths against the
// full schema before removing the path scope; a scoped alias can be ambiguous
// elsewhere in the workspace. This reads no collection keys or artifact values.
func prepareCollectionCLI(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, items []listedArtifact) (map[string]string, error) {
	id, err := dag.CurrentWorkspace().Artifacts().ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct {
			DimensionDefinitions artifact.Dimensions
			PathDefinitions      []struct {
				URI        string
				Dimensions []string
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id: $id) { ... on Artifacts {
  dimensionDefinitions { identifier name qualifiedName }
  pathDefinitions { uri dimensions }
} } }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, dimension := range response.Node.DimensionDefinitions {
		names[dimension.Identifier] = artifactDimensionFlagName(cmd, response.Node.DimensionDefinitions, dimension)
	}
	for i := range response.Node.PathDefinitions {
		addr, err := dagaddress.Parse(response.Node.PathDefinitions[i].URI)
		if err != nil {
			return nil, err
		}
		response.Node.PathDefinitions[i].URI = addr.Path
	}
	// Cache by schema path and dimension identifiers, independent of runtime keys.
	unique := map[string]bool{}
	for i, item := range items {
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return nil, err
		}
		dimensions := make([]string, len(item.DimensionKeys))
		for j, key := range item.DimensionKeys {
			dimensions[j] = key.Dimension
		}
		encoded, _ := json.Marshal(dimensions)
		cacheKey := addr.Path + string(encoded)
		flagsOnly, exists := unique[cacheKey]
		if !exists {
			flagsOnly = true
			for _, path := range response.Node.PathDefinitions {
				if addr.Path == "" || path.URI == addr.Path || strings.HasPrefix(path.URI, addr.Path+"/") {
					continue
				}
				if !slices.ContainsFunc(dimensions, func(d string) bool { return !slices.Contains(path.Dimensions, d) }) {
					flagsOnly = false
					break
				}
			}
			unique[cacheKey] = flagsOnly
		}
		items[i].CLIFlagsOnly = flagsOnly && !addr.Absolute
		// An item type assertion would exclude checks below the collection item.
		// Keep only the path when a collection row still needs a link.
		addr.Types = nil
		items[i].URI = addr.String()
	}
	return names, nil
}
