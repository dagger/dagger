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
  dimensionDefinitions { kind identifier name qualifiedName collectionType itemType keyName keyDescription }
  pathDefinitions(typeAssertion: true) { uri dimensions }
} } }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	return response.Node, err
}

// Collection keys can select the item and its descendants. Omit the item's
// type key only when no other schema path matches those collection dimensions.
// Empty collections count too. Cache the proof per path and dimension set.
func omitCollectionTypeKeys(items []listedArtifact, paths []artifactListPath) error {
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
		if !item.CollectionItem {
			continue
		}
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return err
		}
		var dimensions []string
		for _, key := range item.DimensionKeys {
			if !strings.HasPrefix(key.Dimension, "type:") {
				dimensions = append(dimensions, key.Dimension)
			}
		}
		if len(dimensions) == 0 {
			continue
		}
		slices.Sort(dimensions)
		dimensions = slices.Compact(dimensions)
		cacheKey := addr.Path + "\x00" + strings.Join(dimensions, "\x00")
		if omit, ok := cache[cacheKey]; ok {
			items[i].OmitTypeKey = omit
			continue
		}
		matched, safe := false, true
		for j, path := range paths {
			if slices.ContainsFunc(dimensions, func(d string) bool { return !slices.Contains(path.Dimensions, d) }) {
				continue
			}
			candidate := addresses[j]
			if candidate.Path != addr.Path && !strings.HasPrefix(candidate.Path, addr.Path+"/") {
				safe = false
				break
			}
			matched = true
		}
		cache[cacheKey] = matched && safe
		items[i].OmitTypeKey = matched && safe
	}
	return nil
}

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
