package core

import (
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dagger/dagger/core/artifact"
)

// A module that fails to load has no schema, so its load failure stands in
// for whatever the module would have held. Inclusion filters keep the failure
// when the module could have held something they select, so consumers report
// the load error instead of an incomplete result; FilterTypes does the same.
// Exclusions match the failure only by its own path and keys (see
// Artifacts.exact), so excluding part of a broken module does not hide its
// failure.
//
// Whether a module could hold something follows from how discovery names
// artifacts: paths start with the module's address name (a main's may
// omit it), collection dimension identifiers start with /<module>/ and their
// names with <module>-, and a type dimension's key is an artifact path.

// failedModule is the address name of the module whose load failure a is: the
// first segment of its path. Empty for any other artifact.
func (a *Artifact) failedModule() string {
	if a.LoadFailure == nil || len(a.Path) == 0 {
		return ""
	}
	return a.Path[0]
}

// failedModuleNames are the spellings the failed module's artifacts could
// have: its address name, by the latest naming rules, and by the legacy
// ones when they differ (e-2-e-tests for e2e-tests). A module that failed to
// load has no known engine version, so its rules aren't known.
func (a *Artifact) failedModuleNames() []string {
	module := a.failedModule()
	if module == "" {
		return nil
	}
	if legacy := LegacyNamer.CLIName(module); legacy != module {
		return []string{module, legacy}
	}
	return []string{module}
}

// mayContainPath reports whether a's module could hold an artifact at a path
// that pattern selects. A literal pattern is one path; glob patterns follow
// FilterPattern's rules.
func (a *Artifact) mayContainPath(pattern string, glob bool) bool {
	modules := a.failedModuleNames()
	if len(modules) == 0 {
		return false
	}
	alternatives := []string{pattern}
	if glob {
		alternatives = expandBraces(pattern)
	}
	for _, alternative := range alternatives {
		first, _, _ := strings.Cut(alternative, "/")
		if first == "" {
			continue
		}
		isGlob := glob && strings.ContainsAny(first, "*?[")
		for _, module := range modules {
			if first == module || isGlob && matchSegment(first, module) {
				return true
			}
		}
		// A main's shorthand path starts with any of its fields,
		// which no other module's name can be.
		if a.LoadFailure.Entrypoint && (isGlob || !slices.Contains(a.LoadFailure.Siblings, first)) {
			return true
		}
	}
	return false
}

// mayHaveDimension reports whether a's module could hold an artifact with the
// dimension id (an identifier, or a name the workspace does not have) and,
// for non-nil keys, one of the keys.
func (a *Artifact) mayHaveDimension(id string, keys []string) bool {
	modules := a.failedModuleNames()
	if len(modules) == 0 || keys != nil && len(keys) == 0 {
		return false
	}
	paths := func() bool {
		return keys == nil || slices.ContainsFunc(keys, func(key string) bool { return a.mayContainPath(key, false) })
	}
	namedByModule := func(name string) bool {
		return slices.ContainsFunc(modules, func(module string) bool { return namedBy(name, module) })
	}
	switch {
	case id == artifact.ModuleDimension:
		return keys == nil || moduleKeysMatch(keys, a.ModuleName)
	case strings.HasPrefix(id, "type:"):
		return paths()
	case strings.HasPrefix(id, "/"):
		owner, _, _ := strings.Cut(strings.TrimPrefix(id, "/"), "/")
		return slices.Contains(modules, owner)
	case namedByModule(id):
		// An unknown name of one of the module's collections.
		return true
	case slices.ContainsFunc(a.LoadFailure.Siblings, func(sibling string) bool { return namedBy(id, sibling) }):
		// Another module's collection.
		return false
	default:
		// Any other unknown name can only be one of the module's types,
		// whose keys are artifact paths.
		return paths()
	}
}

// namedBy reports whether a dimension name belongs to module: collection
// names start with their module's name.
func namedBy(name, module string) bool {
	return name == module || strings.HasPrefix(name, module+"-")
}

// moduleKeysMatch reports whether any key of the module dimension names
// module. Keys compare in their address form, kebab-case, by either naming
// rules, so a module configured as my_mod answers to both my_mod and my-mod,
// and e2e-tests to e-2-e-tests too.
func moduleKeysMatch(keys []string, module string) bool {
	return slices.ContainsFunc(keys, func(key string) bool { return SameCLIName(key, module) })
}

// matchSegment matches one path segment against a glob segment.
func matchSegment(pattern, segment string) bool {
	if pattern == "**" {
		return true
	}
	match, err := doublestar.Match(pattern, segment)
	return err == nil && match
}

// expandBraces lists the alternatives of a glob's {a,b} groups, which may
// contain path separators.
func expandBraces(pattern string) []string {
	start := strings.IndexByte(pattern, '{')
	if start < 0 {
		return []string{pattern}
	}
	depth, end := 0, -1
	var commas []int
	for i := start; i < len(pattern) && end < 0; i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		case ',':
			if depth == 1 {
				commas = append(commas, i)
			}
		}
	}
	if end < 0 {
		return []string{pattern}
	}
	var expanded []string
	prev := start + 1
	for _, cut := range append(commas, end) {
		for _, rest := range expandBraces(pattern[prev:cut] + pattern[end+1:]) {
			expanded = append(expanded, pattern[:start]+rest)
		}
		prev = cut + 1
	}
	return expanded
}
