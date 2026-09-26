package daggercmd

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
)

func listArtifactSelection(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, cmd *cobra.Command) error {
	ctx, span := Tracer().Start(ctx, "list artifacts", telemetry.Encapsulate())
	defer span.End()
	absolute, _ := cmd.Flags().GetBool("absolute")
	items, err := readListedArtifactPaths(ctx, dag, selection, absolute)
	if err != nil {
		return err
	}
	names, err := prepareArtifactOutput(ctx, dag, cmd, items)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeArtifactList(cmd, items, names)
}

func readListedArtifactPaths(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, absolute bool) ([]listedArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct {
			PathDefinitions []struct {
				URI, Description, ModuleName string
				Dimensions                   []string
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!) {
  node(id: $id) { ... on Artifacts { pathDefinitions(absolute: $absolute, typeAssertion: true) { uri description dimensions moduleName } } }
 }`, Variables: map[string]any{"id": id, "absolute": absolute}}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	var items []listedArtifact
	for _, path := range response.Node.PathDefinitions {
		item := listedArtifact{URI: path.URI, Description: path.Description}
		addr, err := dagaddress.Parse(path.URI)
		if err != nil {
			return nil, err
		}
		for _, dim := range path.Dimensions {
			if dim == artifact.ModuleDimension {
				item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{dim, path.ModuleName})
			} else if strings.HasPrefix(dim, "type:") {
				item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{dim, addr.Path})
			}
		}
		items = append(items, item)
	}
	return items, nil
}
