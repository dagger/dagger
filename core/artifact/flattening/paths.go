package flattening

import (
	"slices"
	"strings"
)

// Path records a full schema path in a type dimension and its collection dimensions in parent order.
type Path struct {
	Type       string
	Key        string
	Module     string
	Dimensions []string
}

// Paths indexes schema paths across all loaded modules.
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

// Candidates lists sorted, unique paths that have the type and all dimensions.
// A nonempty module list limits the results to those modules.
func (paths Paths) Candidates(typeDimension string, dimensions, modules []string) []string {
	var keys []string
	for _, path := range paths.CandidatePaths(typeDimension, dimensions, modules) {
		keys = append(keys, path.Key)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// CandidatePaths returns entries that have the type, all dimensions, and a matching module when specified.
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

// MatchTypeKey returns an exact path match, or paths with the given suffix.
// A leading "/" requires an exact match.
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

// ShortTypeKey returns the shortest unique path suffix.
// It uses a leading "/" when the full path is a suffix of another candidate.
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
