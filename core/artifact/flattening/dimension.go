// Package flattening defines artifact dimension and path flattening.
package flattening

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const ModuleDimension = "module"

// IsStaticDimension identifies keys available without loading collections.
func IsStaticDimension(identifier string) bool {
	return identifier == ModuleDimension || identifier == NameDimension || strings.HasPrefix(identifier, "type:")
}

// Dimension describes a schema axis, independent of its runtime keys.
type Dimension struct {
	// Kind is MODULE, NAME, COLLECTION, or legacy TYPE.
	Kind string
	// CollectionType is the collection object type, not its item type.
	CollectionType string
	// Identifier is the full identity. Names below are selector aliases.
	Identifier    string
	Name          string
	QualifiedName string
	// ItemType is the collection item or legacy artifact type. Static module
	// and name dimensions have none.
	ItemType string
	// KeyName and KeyDescription describe the authored lookup argument.
	// Static dimensions use "name" and an empty description.
	KeyName        string
	KeyDescription string
}

// Dimensions is the set of dimensions in a selected schema scope.
type Dimensions []*Dimension

// Union combines dimension sets, sorted by identifier.
func Union(sets ...Dimensions) Dimensions {
	byID := map[string]*Dimension{}
	for _, set := range sets {
		for _, dim := range set {
			byID[dim.Identifier] = dim
		}
	}
	result := make(Dimensions, 0, len(byID))
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		result = append(result, byID[id])
	}
	return result
}

// NameDimensions labels the complete canonical column set. Collections use
// their item type name until it conflicts with another column or a fixed name.
// Every conflicting collection then uses its collection type name. Repeat for
// new conflicts; fail when no further fallback is possible. Legacy dimensions
// retain their names. The result owns its metadata and is sorted by identifier.
func NameDimensions(dimensions Dimensions) (Dimensions, error) {
	result := make(Dimensions, len(dimensions))
	canonical := make([]bool, len(dimensions))
	fallback := make([]bool, len(dimensions))
	for i, dimension := range dimensions {
		copy := *dimension
		result[i] = &copy
		canonical[i] = copy.Kind == "COLLECTION" && copy.CollectionType != "" && copy.Identifier == copy.CollectionType
		if canonical[i] {
			copy.Name = latestName(copy.ItemType)
		}
	}
	for {
		groups := map[string][]int{ModuleDimension: {-1}, NameDimension: {-1}}
		for i, dimension := range result {
			if canonical[i] {
				groups[dimension.Name] = append(groups[dimension.Name], i)
			}
		}
		conflicts := []string{}
		changed := false
		for _, name := range slices.Sorted(maps.Keys(groups)) {
			group := groups[name]
			if len(group) < 2 {
				continue
			}
			conflicts = append(conflicts, name)
			for _, i := range group {
				if i >= 0 && !fallback[i] {
					result[i].Name = latestName(result[i].CollectionType)
					fallback[i] = true
					changed = true
				}
			}
		}
		if len(conflicts) == 0 {
			// Exact identifiers remain valid selectors. A label must not
			// select another column through that identifier precedence.
			for i, dimension := range result {
				if !canonical[i] {
					continue
				}
				for j, other := range result {
					if canonical[j] && dimension.Identifier != other.Identifier && dimension.Name == other.Identifier {
						return nil, fmt.Errorf("dimension name %q conflicts with collection identifier %s", dimension.Name, other.Identifier)
					}
				}
			}
			slices.SortFunc(result, func(a, b *Dimension) int { return strings.Compare(a.Identifier, b.Identifier) })
			return result, nil
		}
		if !changed {
			return nil, fmt.Errorf("conflicting dimension names after collection type fallback: %s", strings.Join(conflicts, ", "))
		}
	}
}

// Names lists selectors from shortest to most qualified. Collection boundaries
// provide parent identity; their fields are needed in names only on a conflict.
func (dims Dimensions) Names(dim *Dimension, wholeCollection bool) []string {
	if IsStaticDimension(dim.Identifier) {
		return []string{dim.Name, dim.QualifiedName}
	}
	if dim.CollectionType != "" && dim.Identifier == dim.CollectionType {
		if wholeCollection {
			return []string{dim.QualifiedName}
		}
		return []string{dim.Name, dim.QualifiedName}
	}
	path := strings.Split(strings.TrimPrefix(dim.Identifier, "/"), "/")
	module := path[0]
	item := strings.TrimPrefix(dim.Name, module+"-")
	if wholeCollection {
		if len(path) == 1 {
			return []string{module}
		}
		item = path[len(path)-1]
	}
	var names []string
	add := func(qualifiers []string) {
		parts := append([]string{module}, qualifiers...)
		parts = append(parts, item)
		name := strings.Join(parts, "-")
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	add(nil)
	if len(path) == 1 {
		return names
	}
	var local []string
	for i := 1; i < len(path)-1; i++ {
		parent := strings.Join(path[:i+1], "/")
		if !slices.ContainsFunc(dims, func(d *Dimension) bool { return strings.TrimPrefix(d.Identifier, "/") == parent }) {
			local = append(local, path[i])
		}
	}
	for i := len(local) - 1; i >= 0; i-- {
		add(local[i:])
	}
	// Include parent collection names, then this collection field, only if the
	// local names above cannot distinguish the routes.
	for _, context := range [][]string{path[1 : len(path)-1], path[1:]} {
		for i := len(context) - 1; i >= 0; i-- {
			add(context[i:])
		}
	}
	return names
}

// Resolve binds an identifier or alias. Unknown names remain valid selectors
// that match no artifacts.
func (dims Dimensions) Resolve(name string) (string, error) {
	for _, dim := range dims {
		if dim.Identifier == name {
			return name, nil
		}
	}
	var canonical []string
	for _, dim := range dims {
		if dim.Kind == "COLLECTION" && dim.Identifier == dim.CollectionType && dim.Name == name {
			canonical = append(canonical, dim.Identifier)
		}
	}
	if len(canonical) == 1 {
		return canonical[0], nil
	}
	if len(canonical) > 1 {
		return "", fmt.Errorf("ambiguous dimension %q: use %s", name, strings.Join(canonical, " or "))
	}
	var matches, types []string
	qualified := slices.ContainsFunc(dims, func(d *Dimension) bool { return d.QualifiedName == name })
	for _, dim := range dims {
		if qualified && dim.QualifiedName != name {
			continue
		}
		matchesName := dim.Name == name || dim.QualifiedName == name || slices.Contains(dims.Names(dim, false), name)
		if !matchesName && !IsStaticDimension(dim.Identifier) {
			matchesName = slices.Contains(dims.Names(dim, true), name)
		}
		if matchesName {
			if dim.Kind == "TYPE" {
				types = append(types, dim.Identifier)
			} else {
				matches = append(matches, dim.Identifier)
			}
		}
	}
	// Collection aliases retain their meaning when an item type has the same name.
	if len(matches) == 0 {
		matches = types
	}
	switch len(matches) {
	case 0:
		return name, nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous dimension %q: use %s", name, strings.Join(matches, " or "))
	}
}

// DisplayName returns the shortest unambiguous name for a dimension.
func (dims Dimensions) DisplayName(dim *Dimension) string {
	for _, alias := range append([]string{dim.Name}, dims.Names(dim, false)...) {
		if id, err := dims.Resolve(alias); err == nil && id == dim.Identifier {
			return alias
		}
	}
	return dim.Identifier
}
