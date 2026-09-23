package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
)

// Compare canonical keys, since dimension aliases depend on the selected scope.
func listedArtifactIDs(items []listedArtifact) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		addr, _ := dagaddress.Parse(item.URI)
		keys := slices.Clone(item.DimensionKeys)
		slices.SortFunc(keys, func(a, b struct{ Dimension, Key string }) int { return strings.Compare(a.Dimension, b.Dimension) })
		encoded, _ := json.Marshal(keys)
		ids = append(ids, addr.Path+string(encoded))
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

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
	if !all && !filtered {
		return listArtifactPaths(ctx, dag, selection, cmd, absolute)
	}
	items, err := readListedArtifacts(ctx, dag, selection, absolute, true)
	if err != nil {
		return err
	}
	var allArtifacts, targets *dagger.Artifacts
	names := map[string]string{}
	if len(listedArtifactKeys(items)) > 0 {
		// Resolve discovery once. Every formatting query must use this same set,
		// since currentWorkspace has a new identity on each call.
		id, err := dag.CurrentWorkspace().Artifacts().ID(ctx)
		if err != nil {
			return err
		}
		allArtifacts = dagger.Ref[*dagger.Artifacts](dag, id)
		defs, err := artifactDimensions(ctx, dag, allArtifacts)
		if err != nil {
			return err
		}
		for _, def := range defs {
			names[def.Identifier] = artifactDimensionFlagName(cmd, defs, def)
		}
		targets, err = commandArtifactTargets(dag, cmd, allArtifacts)
		if err != nil {
			return err
		}
	}
	candidates := map[string][]string{}
	// Read only candidate paths and keys to check whether rows can be grouped.
	matches := func(path string, keys []dagaddress.Pair, want []listedArtifact) bool {
		encoded, _ := json.Marshal(keys)
		cacheKey := path + string(encoded)
		if got, ok := candidates[cacheKey]; ok {
			return slices.Equal(got, listedArtifactIDs(want))
		}
		address, err := dagaddress.Parse(path)
		if err != nil {
			return false
		}
		address.Absolute = false
		address.Query = keys
		candidate := targets.FilterURI(address.String())
		got, err := readListedArtifacts(ctx, dag, candidate, false, false)
		if err != nil {
			return false
		}
		candidates[cacheKey] = listedArtifactIDs(got)
		return slices.Equal(candidates[cacheKey], listedArtifactIDs(want))
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
	paths := slices.Sorted(maps.Keys(groups))
	var output []listedArtifact
	for _, path := range paths {
		for _, row := range artifactListRows(path, groups[path], all, filtered, matches) {
			item := listedArtifact{URI: path, Description: row[0].Description}
			for _, key := range listedArtifactKeys(row) {
				item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
			}
			output = append(output, item)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeArtifactCLI(cmd.OutOrStdout(), output, names, artifactListReplayArgs(cmd)); err != nil {
		return err
	}
	if len(output) < len(items) {
		_, err = fmt.Fprintln(cmd.ErrOrStderr(), "# Use --all to expand collections and list each item.")
	}
	return err
}

func listArtifactPaths(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, cmd *cobra.Command, absolute bool) error {
	id, err := selection.ID(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Node struct {
			PathDefinitions []struct {
				URI, Description string
				Dimensions       []string
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!) {
  node(id: $id) { ... on Artifacts { pathDefinitions(absolute: $absolute, typeAssertion: true) { uri description dimensions } } }
 }`, Variables: map[string]any{"id": id, "absolute": absolute}}, &dagger.Response{Data: &response})
	if err != nil {
		return err
	}
	var items []listedArtifact
	hasDimensions := false
	for _, path := range response.Node.PathDefinitions {
		items = append(items, listedArtifact{URI: path.URI, Description: path.Description})
		hasDimensions = hasDimensions || len(path.Dimensions) > 0
	}
	if err := writeArtifactCLI(cmd.OutOrStdout(), items, nil, artifactListReplayArgs(cmd)); err != nil {
		return err
	}
	if hasDimensions {
		_, err = fmt.Fprintln(cmd.ErrOrStderr(), "# Use --all to expand collections and list each item.")
	}
	return err
}

// Keep explicit input filters even if they include every current key.
func artifactListHasKeyFilters(cmd *cobra.Command) (bool, error) {
	keys, err := artifactKeyFlags(cmd)
	if err != nil {
		return false, err
	}
	addresses, err := parseArtifactAddresses(cmd.Flags().Args())
	if err != nil {
		return false, err
	}
	for _, address := range addresses {
		if len(address.Query) > 0 {
			return true, nil
		}
	}
	return len(keys) > 0, nil
}

// Collapse a path only when the resulting command selects the same items.
func artifactListRows(
	path string,
	group []listedArtifact,
	all, filtered bool,
	matches func(string, []dagaddress.Pair, []listedArtifact) bool,
) [][]listedArtifact {
	if !all && len(group) > 1 && (!filtered && matches(path, nil, group) || matches(path, listedArtifactKeys(group), group)) {
		return [][]listedArtifact{group}
	}
	rows := make([][]listedArtifact, 0, len(group))
	for _, item := range group {
		rows = append(rows, []listedArtifact{item})
	}
	return rows
}
