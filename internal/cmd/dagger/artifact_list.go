package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
)

func listedArtifactKeys(items []listedArtifact) []dagaddress.Pair {
	var keys []dagaddress.Pair
	for _, item := range items {
		for _, key := range item.DimensionKeys {
			pair := dagaddress.Pair{Dimension: key.Dimension, Key: key.Key, HasKey: true}
			if !slices.Contains(keys, pair) {
				keys = append(keys, pair)
			}
		}
	}
	return keys
}

func listArtifactSelection(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, cmd *cobra.Command) error {
	ctx, span := Tracer().Start(ctx, "list artifacts", telemetry.Encapsulate())
	defer span.End()
	absolute, _ := cmd.Flags().GetBool("absolute")
	all, _ := cmd.Flags().GetBool("all")
	filtered, err := artifactListHasKeyFilters(cmd)
	if err != nil {
		return err
	}
	var items []listedArtifact
	if collectionType := cmd.Annotations[artifactListCollection]; collectionType != "" {
		definitions, err := artifactDimensions(ctx, dag, selection)
		if err != nil {
			return err
		}
		for _, dimension := range definitions {
			if dimension.CollectionType != collectionType {
				continue
			}
			projected, err := readListedDimensionItems(ctx, dag, selection, dimension.Identifier, absolute, all || filtered)
			if err != nil {
				return err
			}
			items = append(items, projected...)
		}
	} else if all || filtered {
		items, err = readListedArtifacts(ctx, dag, selection, absolute, true)
	} else {
		items, err = readListedArtifactPaths(ctx, dag, selection, absolute, "")
	}
	if err != nil {
		return err
	}
	groups := map[string][]listedArtifact{}
	for _, item := range items {
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return err
		}
		addr.Query = nil
		path := addr.String()
		groups[path] = append(groups[path], item)
	}
	groupPaths := slices.Sorted(maps.Keys(groups))
	var output []listedArtifact
	for _, path := range groupPaths {
		for _, row := range artifactListRows(groups[path], all) {
			item := row[0]
			item.DimensionKeys = nil
			for _, key := range listedArtifactKeys(row) {
				item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
			}
			if len(row) > 1 {
				addr, err := dagaddress.Parse(path)
				if err != nil {
					return err
				}
				for _, key := range item.DimensionKeys {
					if !artifact.IsStaticDimension(key.Dimension) {
						addr.Query = append(addr.Query, dagaddress.Pair{Dimension: key.Dimension, Key: key.Key, HasKey: true})
					}
				}
				item.URI = addr.String()
			}
			output = append(output, item)
		}
	}
	names, err := prepareArtifactOutput(ctx, dag, cmd, output)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeArtifactList(cmd, output, names); err != nil {
		return err
	}
	if len(output) < len(items) || slices.ContainsFunc(output, func(item listedArtifact) bool { return len(item.Presence) > 0 }) {
		_, err = fmt.Fprintln(cmd.ErrOrStderr(), "# Use --all to expand collections and list each item.")
	}
	return err
}

func readListedArtifactPaths(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, absolute bool, dimension string) ([]listedArtifact, error) {
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
	variables := map[string]any{"id": id, "absolute": absolute}
	if dimension != "" {
		variables["dimension"] = dimension
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!, $dimension: String) {
  node(id: $id) { ... on Artifacts { pathDefinitions(absolute: $absolute, typeAssertion: true, dimension: $dimension) { uri description dimensions moduleName } } }
 }`, Variables: variables}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	var items []listedArtifact
	for _, path := range response.Node.PathDefinitions {
		item := listedArtifact{URI: path.URI, Description: path.Description, CollectionItem: dimension != ""}
		addr, err := dagaddress.Parse(path.URI)
		if err != nil {
			return nil, err
		}
		for _, dim := range path.Dimensions {
			if dim == artifact.ModuleDimension {
				item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{dim, path.ModuleName})
			} else if strings.HasPrefix(dim, "type:") {
				item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{dim, addr.Path})
			} else {
				item.Presence = append(item.Presence, dim)
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// Keep explicit input filters even if they include every current key.
func artifactListHasKeyFilters(cmd *cobra.Command) (bool, error) {
	keys := artifactKeyFlags(cmd)
	addresses, err := parseArtifactAddresses(cmd.Flags().Args())
	if err != nil {
		return false, err
	}
	for _, address := range addresses {
		if slices.ContainsFunc(address.Query, func(p dagaddress.Pair) bool { return p.HasKey && !artifact.IsStaticDimension(p.Dimension) }) {
			return true, nil
		}
	}
	return slices.ContainsFunc(keys, func(p dagaddress.Pair) bool { return p.HasKey && !artifact.IsStaticDimension(p.Dimension) }), nil
}

// Collapse only products that can be expressed with repeated dimension flags.
func artifactListRows(group []listedArtifact, all bool) [][]listedArtifact {
	if !all && len(group) > 1 && artifactRowsFormProduct(group) {
		return [][]listedArtifact{group}
	}
	rows := make([][]listedArtifact, 0, len(group))
	for _, item := range group {
		rows = append(rows, []listedArtifact{item})
	}
	return rows
}
