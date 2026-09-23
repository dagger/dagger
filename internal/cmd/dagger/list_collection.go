package daggercmd

import (
	"context"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

// Collection rows select an item and its descendants. Use workspace-wide flag
// names and schema scope, even if the list itself was narrowed by a link.
func prepareCollectionCLI(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, items []listedArtifact) (map[string]string, error) {
	schema, err := readArtifactListSchema(ctx, dag, dag.CurrentWorkspace().Artifacts())
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, dimension := range schema.DimensionDefinitions {
		names[dimension.Identifier] = artifactDimensionFlagName(cmd, schema.DimensionDefinitions, dimension)
	}
	return names, projectArtifactLinks(items, schema.PathDefinitions)
}
