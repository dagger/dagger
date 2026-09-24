package daggercmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/dagaddress"
)

type artifactNamedPath struct {
	key        string
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
				candidate := artifactNamedPath{address.Path, path.Dimensions}
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
	var candidates []string
	for _, path := range index[dimension] {
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

func (index artifactNameIndex) resolve(dimension, name string, filters []dagaddress.Pair) (string, error) {
	var matches []string
	for _, key := range index.candidates(dimension, filters) {
		if artifactNameMatches(key, name) {
			matches = append(matches, key)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no %s matches %q", strings.TrimPrefix(dimension, "type:"), name)
	case 1:
		return matches[0], nil
	default:
		for i := range matches {
			matches[i] = "/" + matches[i]
		}
		return "", fmt.Errorf("ambiguous %s %q; use %s", strings.TrimPrefix(dimension, "type:"), name, strings.Join(matches, " or "))
	}
}

func (index artifactNameIndex) short(dimension, key string, filters []dagaddress.Pair) string {
	fields := strings.Split(key, "/")
	for i := len(fields) - 1; i >= 0; i-- {
		name := strings.Join(fields[i:], "/")
		if resolved, err := index.resolve(dimension, name, filters); err == nil && resolved == key {
			return name
		}
	}
	return "/" + key
}

func resolveArtifactTypeKeys(paths []artifactListPath, filters []dagaddress.Pair) error {
	index, err := newArtifactNameIndex(paths)
	if err != nil {
		return err
	}
	for i, filter := range filters {
		if !filter.HasKey || !strings.HasPrefix(filter.Dimension, "type:") {
			continue
		}
		key, err := index.resolve(filter.Dimension, filter.Key, filters)
		if err != nil {
			return err
		}
		filters[i].Key = key
	}
	return nil
}
