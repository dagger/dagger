package artifact

import (
	"slices"
	"strings"
)

// Path is one schema path in a type dimension: the full path its type key
// holds, and the collection dimensions an address for it must key.
type Path struct {
	// Type is the type dimension identifier, such as type:Check.
	Type   string
	Key    string
	Module string
	// Dimensions are collection dimension identifiers, parent to child.
	Dimensions []string
}

// Paths indexes the schema paths of every loaded module, so a type key names
// one path in a workspace whatever the selection.
type Paths []Path

// UnionPaths combines path sets, dropping duplicates.
func UnionPaths(sets ...Paths) Paths {
	var result Paths
	seen := map[string]bool{}
	for _, set := range sets {
		for _, path := range set {
			key := path.Type + "\x00" + path.Key + "\x00" + path.Module + "\x00" + strings.Join(path.Dimensions, "\x00")
			if !seen[key] {
				seen[key] = true
				result = append(result, path)
			}
		}
	}
	return result
}

// Candidates lists the full paths in a type dimension that an address keying
// dimensions can name. A path qualifies when it has every one of them. Modules
// narrow the list when set.
func (paths Paths) Candidates(typeDimension string, dimensions, modules []string) []string {
	var keys []string
	for _, path := range paths.CandidatePaths(typeDimension, dimensions, modules) {
		keys = append(keys, path.Key)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// CandidatePaths is Candidates with each path's dimensions.
func (paths Paths) CandidatePaths(typeDimension string, dimensions, modules []string) Paths {
	var candidates Paths
	for _, path := range paths {
		if path.Type != typeDimension || len(modules) > 0 && !slices.Contains(modules, path.Module) {
			continue
		}
		if slices.ContainsFunc(dimensions, func(d string) bool { return !slices.Contains(path.Dimensions, d) }) {
			continue
		}
		candidates = append(candidates, path)
	}
	return candidates
}

// MatchTypeKey lists the candidates a type key names: the path it equals, and
// each path it is a suffix of, on segment boundaries. A leading "/" accepts
// only a full path.
func MatchTypeKey(candidates []string, key string) []string {
	if exact, ok := strings.CutPrefix(key, "/"); ok {
		if slices.Contains(candidates, exact) {
			return []string{exact}
		}
		return nil
	}
	var matches []string
	for _, candidate := range candidates {
		if candidate == key || strings.HasSuffix(candidate, "/"+key) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

// ResolveTypeKey lists the full paths a type key selects among candidates.
func ResolveTypeKey(candidates Paths, key string) []string {
	var keys []string
	for _, path := range candidates {
		keys = append(keys, path.Key)
	}
	slices.Sort(keys)
	return MatchTypeKey(slices.Compact(keys), key)
}

// ShortTypeKey is the shortest suffix of path that names only path among the
// candidates. Without one, it is the full path, or the full path with a
// leading "/" when that is also the suffix of a longer candidate. It is a
// projection: adding a path to the schema can lengthen it.
func ShortTypeKey(candidates []string, path string) string {
	if !slices.Contains(candidates, path) {
		return path
	}
	fields := strings.Split(path, "/")
	for i := len(fields) - 1; i >= 0; i-- {
		short := strings.Join(fields[i:], "/")
		if matches := MatchTypeKey(candidates, short); len(matches) == 1 && matches[0] == path {
			return short
		}
	}
	return "/" + path
}
