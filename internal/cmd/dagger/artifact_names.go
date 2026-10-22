package daggercmd

import (
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger/core"

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
				candidate := artifactNamedPath{key: address.ArtifactPath(address.Types...), module: path.ModuleName, dimensions: path.Dimensions}
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

// candidatePaths is candidates with each path's collection dimensions.
func (index artifactNameIndex) candidatePaths(dimension string, filters []dagaddress.Pair) artifact.Paths {
	keys := index.candidates(dimension, filters)
	var paths artifact.Paths
	for _, path := range index[dimension] {
		if !slices.Contains(keys, path.key) {
			continue
		}
		candidate := artifact.Path{Type: dimension, Key: path.key, Module: path.module}
		for _, d := range path.dimensions {
			if !artifact.IsStaticDimension(d) {
				candidate.Dimensions = append(candidate.Dimensions, d)
			}
		}
		paths = append(paths, candidate)
	}
	return paths
}

func (index artifactNameIndex) matches(dimension, name string, filters []dagaddress.Pair) []string {
	return artifact.MatchTypeKey(index.candidates(dimension, filters), name)
}

func (index artifactNameIndex) short(dimension, key string, filters []dagaddress.Pair) string {
	return artifact.ShortTypeKey(index.candidates(dimension, filters), key)
}

// Apply type-key filters through the API so an empty match stays an empty
// filter, which a link query cannot represent.
func filterArtifactTypeKeys(artifacts *core.Artifacts, paths []artifactListPath, filter *dagaddress.Address) (*core.Artifacts, error) {
	index, err := newArtifactNameIndex(paths)
	if err != nil {
		return nil, err
	}
	keys := map[string][]string{}
	for _, pair := range filter.Query {
		if !pair.HasKey || !strings.HasPrefix(pair.Dimension, "type:") {
			continue
		}
		matches := artifact.ResolveTypeKey(index.candidatePaths(pair.Dimension, filter.Query), pair.Key)
		// A key that names nothing leaves the filter empty.
		keys[pair.Dimension] = append(keys[pair.Dimension], matches...)
		if keys[pair.Dimension] == nil {
			keys[pair.Dimension] = []string{}
		}
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
