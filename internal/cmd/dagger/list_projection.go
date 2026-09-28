package daggercmd

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
)

type artifactListPath struct {
	URI        string
	ModuleName string
	LoadError  string
	Dimensions []string
}

func readArtifactListPaths(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts) ([]artifactListPath, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct{ PathDefinitions []artifactListPath }
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id: $id) { ... on Artifacts {
  pathDefinitions(typeAssertion: true) { uri dimensions loadError moduleName }
} } }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	return response.Node.PathDefinitions, err
}

// Share this schema proof across rows, while preserving module values and the
// exact target type and path.
func omitArtifactCLITypeKeys(items []listedArtifact, index artifactNameIndex, types []string) {
	cache := map[string]bool{}
	for i, item := range items {
		filters := itemDimensionFilters(item)
		slices.SortFunc(filters, func(a, b dagaddress.Pair) int {
			if order := strings.Compare(a.Dimension, b.Dimension); order != 0 {
				return order
			}
			return strings.Compare(a.Key, b.Key)
		})
		encoded, _ := json.Marshal(slices.Compact(filters))
		key := string(encoded)
		omit, ok := cache[key]
		if !ok {
			omit = canOmitArtifactCLITypeKey(item, index, types)
			cache[key] = omit
		}
		items[i].OmitCLITypeKey = omit
	}
}

// Prove redundancy against the full workspace schema and the command's fixed
// type scope. Keep module selectors, including every key value. Input filters
// cannot narrow this proof.
func canOmitArtifactCLITypeKey(item listedArtifact, index artifactNameIndex, types []string) bool {
	var target dagaddress.Pair
	var filters []dagaddress.Pair
	for _, filter := range itemDimensionFilters(item) {
		if strings.HasPrefix(filter.Dimension, "type:") {
			if target.Dimension != "" && target != filter || !filter.HasKey {
				return false
			}
			target = filter
		} else {
			filters = append(filters, filter)
		}
	}
	if target.Dimension == "" || len(filters) == 0 {
		return false
	}
	matched := false
	for dimension := range index {
		if len(types) > 0 && !slices.Contains(types, strings.TrimPrefix(dimension, "type:")) {
			continue
		}
		for _, key := range index.candidates(dimension, filters) {
			if dimension != target.Dimension || key != target.Key {
				return false
			}
			matched = true
		}
	}
	return matched
}
