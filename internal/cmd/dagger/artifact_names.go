package daggercmd

import (
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
)

type artifactNamedPath struct {
	key        string
	module     string
	dimensions []string
}

// One index serves input resolution and output naming. Empty collections count.
type artifactNameIndex map[string][]artifactNamedPath

func newArtifactNameIndex(paths []artifactListPath) (artifactNameIndex, error) {
	index := artifactNameIndex{}
	for _, path := range paths {
		address, err := dagaddress.Parse(path.URI)
		if err != nil {
			return nil, err
		}
		for _, dimension := range path.Dimensions {
			if strings.HasPrefix(dimension, "type:") {
				candidate := artifactNamedPath{key: address.Path, module: path.ModuleName, dimensions: path.Dimensions}
				if !slices.ContainsFunc(index[dimension], func(p artifactNamedPath) bool {
					return p.key == candidate.key && slices.Equal(p.dimensions, candidate.dimensions)
				}) {
					index[dimension] = append(index[dimension], candidate)
				}
			}
		}
	}
	return index, nil
}

func (index artifactNameIndex) candidates(dimension string, filters []dagaddress.Pair) []string {
	var modules []string
	for _, filter := range filters {
		if filter.Dimension == artifact.ModuleDimension && filter.HasKey {
			modules = append(modules, filter.Key)
		}
	}
	var candidates []string
	for _, path := range index[dimension] {
		if len(modules) > 0 && !slices.Contains(modules, path.module) {
			continue
		}
		if slices.ContainsFunc(filters, func(filter dagaddress.Pair) bool {
			return !slices.Contains(path.dimensions, filter.Dimension)
		}) {
			continue
		}
		candidates = append(candidates, path.key)
	}
	slices.Sort(candidates)
	return slices.Compact(candidates)
}

func artifactNameMatches(key, name string) bool {
	if strings.HasPrefix(name, "/") {
		return key == strings.TrimPrefix(name, "/")
	}
	return key == name || strings.HasSuffix(key, "/"+name)
}

func (index artifactNameIndex) matches(dimension, name string, filters []dagaddress.Pair) []string {
	var matches []string
	for _, key := range index.candidates(dimension, filters) {
		if artifactNameMatches(key, name) {
			matches = append(matches, key)
		}
	}
	return matches
}

func (index artifactNameIndex) short(dimension, key string, filters []dagaddress.Pair) string {
	fields := strings.Split(key, "/")
	for i := len(fields) - 1; i >= 0; i-- {
		name := strings.Join(fields[i:], "/")
		if matches := index.matches(dimension, name, filters); len(matches) == 1 && matches[0] == key {
			return name
		}
	}
	return "/" + key
}

// Resolve short type keys as sets. Apply them through the API so an empty
// match stays an empty filter, which a link query cannot represent.
func filterArtifactTypeKeys(artifacts *dagger.Artifacts, paths []artifactListPath, filter *dagaddress.Address) (*dagger.Artifacts, error) {
	index, err := newArtifactNameIndex(paths)
	if err != nil {
		return nil, err
	}
	keys := map[string][]string{}
	for _, pair := range filter.Query {
		if !pair.HasKey || !strings.HasPrefix(pair.Dimension, "type:") {
			continue
		}
		keys[pair.Dimension] = append(keys[pair.Dimension], index.matches(pair.Dimension, pair.Key, filter.Query)...)
	}
	for _, dimension := range slices.Sorted(maps.Keys(keys)) {
		values := append([]string{}, keys[dimension]...)
		slices.Sort(values)
		artifacts = artifacts.FilterDimensionKeys(dimension, slices.Compact(values))
	}
	filter.Query = slices.DeleteFunc(filter.Query, func(pair dagaddress.Pair) bool {
		return pair.HasKey && strings.HasPrefix(pair.Dimension, "type:")
	})
	return artifacts, nil
}
