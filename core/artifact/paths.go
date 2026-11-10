package artifact

import "github.com/dagger/dagger/core/artifact/flattening"

// Path is one schema path in a type dimension: the full path its type key
// holds, and the collection dimensions an address for it must key.
type Path = flattening.Path

// Paths indexes the schema paths of every loaded module, so a type key names
// one path in a workspace whatever the selection.
type Paths = flattening.Paths

// UnionPaths combines path sets, dropping duplicates.
func UnionPaths(sets ...Paths) Paths {
	return flattening.UnionPaths(sets...)
}

// MatchTypeKey lists the candidates a type key names: the path it equals, and
// each path it is a suffix of, on segment boundaries. A leading "/" accepts
// only a full path.
func MatchTypeKey(candidates []string, key string) []string {
	return flattening.MatchTypeKey(candidates, key)
}

// ResolveTypeKey lists the full paths a type key selects among candidates.
func ResolveTypeKey(candidates Paths, key string) []string {
	return flattening.ResolveTypeKey(candidates, key)
}

// ShortTypeKey is the shortest suffix of path that names only path among the
// candidates. Without one, it is the full path, or the full path with a
// leading "/" when that is also the suffix of a longer candidate. It is a
// projection: adding a path to the schema can lengthen it.
func ShortTypeKey(candidates []string, path string) string {
	return flattening.ShortTypeKey(candidates, path)
}
