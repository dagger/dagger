package daggercmd

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
)

// This is schema metadata. Reading it must not construct collections.
type artifactListPath struct {
	URI        string
	Dimensions []string
}

type artifactListSchema struct {
	DimensionDefinitions artifact.Dimensions
	PathDefinitions      []artifactListPath
}

func readArtifactListSchema(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts) (artifactListSchema, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return artifactListSchema{}, err
	}
	var response struct{ Node artifactListSchema }
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id: $id) { ... on Artifacts {
  dimensionDefinitions { identifier name qualifiedName }
  pathDefinitions(typeAssertion: true) { uri dimensions }
} } }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	return response.Node, err
}

// Prove link omission against the full schema scope, not the displayed rows.
// Key values do not affect this proof: empty sibling collections count too.
// Cache per matrix so more keys do not repeat the schema scan.
func projectArtifactLinks(items []listedArtifact, paths []artifactListPath) error {
	addresses := make([]*dagaddress.Address, len(paths))
	for i, path := range paths {
		addr, err := dagaddress.Parse(path.URI)
		if err != nil {
			return err
		}
		addresses[i] = addr
	}
	cache := map[string]bool{}
	for i, item := range items {
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return err
		}
		items[i].CLIFlagsOnly = false
		if addr.Absolute || len(item.DimensionKeys) == 0 {
			continue
		}
		var dimensions []string
		for _, key := range item.DimensionKeys {
			dimensions = append(dimensions, key.Dimension)
		}
		slices.Sort(dimensions)
		dimensions = slices.Compact(dimensions)
		addr.Query = nil
		encoded, _ := json.Marshal(struct {
			Link       string
			Dimensions []string
			Collection bool
		}{addr.String(), dimensions, item.CollectionItem})
		cacheKey := string(encoded)
		if omit, ok := cache[cacheKey]; ok {
			items[i].CLIFlagsOnly = omit
			continue
		}
		matched, safe := false, true
		for j, path := range paths {
			if slices.ContainsFunc(dimensions, func(d string) bool { return !slices.Contains(path.Dimensions, d) }) {
				continue
			}
			candidate := addresses[j]
			inside := candidate.Path == addr.Path
			if item.CollectionItem {
				inside = inside || strings.HasPrefix(candidate.Path, addr.Path+"/")
			} else {
				inside = inside && slices.Equal(candidate.Types, addr.Types)
			}
			if !inside || candidate.Absolute {
				safe = false
				break
			}
			matched = true
		}
		cache[cacheKey] = matched && safe
		items[i].CLIFlagsOnly = matched && safe
	}
	return nil
}

// Repeated flags form a Cartesian product: alternatives within a dimension,
// AND across dimensions. Collapse only complete products. A sparse selection
// stays as separate rows, so copying it cannot introduce unselected pairs.
func artifactRowsFormProduct(items []listedArtifact) bool {
	if len(items) == 0 {
		return false
	}
	dimensions := map[string]map[string]bool{}
	for _, key := range items[0].DimensionKeys {
		dimensions[key.Dimension] = map[string]bool{}
	}
	rows := map[string]bool{}
	for _, item := range items {
		keys := map[string]string{}
		for _, key := range item.DimensionKeys {
			values, ok := dimensions[key.Dimension]
			if !ok {
				return false
			}
			if _, repeated := keys[key.Dimension]; repeated {
				return false
			}
			values[key.Key] = true
			keys[key.Dimension] = key.Key
		}
		if len(keys) != len(dimensions) {
			return false
		}
		encoded, _ := json.Marshal(keys)
		rows[string(encoded)] = true
	}
	product := 1
	for _, values := range dimensions {
		if len(values) == 0 || product > len(rows)/len(values) {
			return false
		}
		product *= len(values)
	}
	return product == len(rows)
}
