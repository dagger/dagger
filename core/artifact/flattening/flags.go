package flattening

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// FlagNames contains the item key and collection presence flag names.
type FlagNames struct{ Key, Presence string }

// DimensionFlags allocates all names together. A flag has the same meaning in every peer command.
// A nil name function uses current artifact spelling.
func DimensionFlags(dimensions Dimensions, reserved map[string]bool, name func(string) string) map[string]FlagNames {
	if name == nil {
		name = latestName
	}
	dimensions = SelectorDimensions(dimensions, name)
	reserved = cloneReserved(reserved)
	reserved[ModuleDimension] = true
	names := map[string]FlagNames{}

	type request struct {
		id         string
		kind       string
		depth      int
		candidates []string
	}
	var requests []request
	dims := slices.Clone(dimensions)
	slices.SortFunc(dims, func(a, b *Dimension) int {
		if depth := cmp.Compare(strings.Count(a.Identifier, "/"), strings.Count(b.Identifier, "/")); depth != 0 {
			return depth
		}
		return strings.Compare(a.Identifier, b.Identifier)
	})
	// Collection keys have priority over type names.
	for _, kind := range []string{"collection-key", "collection-presence", "type"} {
		for _, d := range dims {
			if d.Kind == "MODULE" {
				names[d.Identifier] = FlagNames{Key: "module"}
				continue
			}
			if (kind == "type") != (d.Kind == "TYPE") {
				continue
			}
			presence := kind == "collection-presence"
			candidates := dims.Names(d, presence)
			if kind == "type" {
				candidates = []string{d.Name, "artifact-" + d.Name}
			}
			requests = append(requests, request{d.Identifier, kind, strings.Count(d.Identifier, "/"), candidates})
		}
	}
	for i, req := range requests {
		chosen := ""
		for level, candidate := range req.candidates {
			if candidate == "" || reserved[candidate] || strings.HasPrefix(candidate, "by-") {
				continue
			}
			conflict := false
			for j, other := range requests {
				if i == j || other.kind != req.kind || other.depth > req.depth || level >= len(other.candidates) {
					continue
				}
				if other.candidates[level] == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				chosen = candidate
				break
			}
		}
		if chosen == "" {
			chosen = "dimension-" + name(strings.ReplaceAll(strings.TrimPrefix(req.id, "/"), "/", "-"))
			if req.kind == "collection-presence" {
				chosen += "-all"
			}
			base := chosen
			for n := 2; reserved[chosen]; n++ {
				chosen = fmt.Sprintf("%s-%d", base, n)
			}
		}
		reserved[chosen] = true
		n := names[req.id]
		if req.kind == "collection-presence" {
			n.Presence = chosen
		} else {
			n.Key = chosen
		}
		names[req.id] = n
	}
	return names
}

// ModuleFlags allocates module aliases after dimension flags.
// A nil name function uses current artifact spelling.
func ModuleFlags(modules []string, dimensions Dimensions, reserved map[string]bool, name func(string) string) map[string]string {
	if name == nil {
		name = latestName
	}
	reserved = cloneReserved(reserved)
	reserved[ModuleDimension] = true
	for _, names := range DimensionFlags(dimensions, reserved, name) {
		reserved[names.Key], reserved[names.Presence] = true, true
	}
	unique := map[string]bool{}
	counts := map[string]int{}
	for _, module := range modules {
		if module != "" && !unique[module] {
			unique[module] = true
			counts[name(module)]++
		}
	}
	names := map[string]string{}
	for module := range unique {
		alias := name(module)
		names[module] = "by-" + module
		if alias != "" && !reserved[alias] && !strings.HasPrefix(alias, "by-") && counts[alias] == 1 {
			names[module] = alias
		}
	}
	return names
}

// SelectorDimensions includes the fixed artifact type selectors.
// A nil name function uses current artifact spelling.
func SelectorDimensions(dimensions Dimensions, name func(string) string) Dimensions {
	if name == nil {
		name = latestName
	}
	dimensions = slices.Clone(dimensions)
	for _, typ := range []string{"Check", "Generator", "Service", "Container", "Directory", "Expertise"} {
		id := "type:" + typ
		if !slices.ContainsFunc(dimensions, func(d *Dimension) bool { return d.Identifier == id }) {
			dimensions = append(dimensions, &Dimension{Identifier: id, Kind: "TYPE", Name: name(typ), ItemType: typ})
		}
	}
	return dimensions
}

func cloneReserved(reserved map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(reserved))
	for name, used := range reserved {
		cloned[name] = used
	}
	return cloned
}
