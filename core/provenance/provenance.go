// Package provenance matches workspace input regions without reading files.
package provenance

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	WorkspaceRead = "WORKSPACE_READ"
	ModuleSource  = "MODULE_SOURCE"
)

type Selector struct {
	Path         string
	Include      []string
	Exclude      []string
	Conservative bool
	Kind         string
}

// Path uses coordinates relative to the workspace boundary, never the host.
func Path(value string) (string, error) {
	if strings.Contains(value, "\\") {
		return "", fmt.Errorf("workspace path must use forward slashes")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("workspace path must not contain parent traversal")
		}
	}
	return path.Clean("/" + value), nil
}

func ValidateKinds(kinds []string) error {
	for _, kind := range kinds {
		if kind != WorkspaceRead && kind != ModuleSource {
			return fmt.Errorf("unknown provenance kind %q", kind)
		}
	}
	return nil
}

func Normalize(selectors []Selector) []Selector {
	out := slices.Clone(selectors)
	if out == nil {
		out = []Selector{}
	}
	for i := range out {
		if root, err := Path(out[i].Path); err == nil {
			out[i].Path = root
		}
		out[i].Include = normalizePatterns(out[i].Include)
		out[i].Exclude = normalizePatterns(out[i].Exclude)
	}
	slices.SortFunc(out, func(a, b Selector) int {
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := slices.Compare(a.Include, b.Include); c != 0 {
			return c
		}
		if c := slices.Compare(a.Exclude, b.Exclude); c != 0 {
			return c
		}
		if a.Conservative == b.Conservative {
			return 0
		}
		if a.Conservative {
			return 1
		}
		return -1
	})
	return slices.CompactFunc(out, func(a, b Selector) bool {
		return a.Path == b.Path && a.Kind == b.Kind && a.Conservative == b.Conservative && slices.Equal(a.Include, b.Include) && slices.Equal(a.Exclude, b.Exclude)
	})
}
func normalizePatterns(patterns []string) []string {
	out := slices.Clone(patterns)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// AffectedByPath uses subtree overlap. A glob can prove that an individual
// path matches; it cannot in general prove that all descendants are absent.
// Keep ambiguous directory regions, so matching never drops a possible input.
func AffectedByPath(selectors []Selector, value string, kinds []string) (bool, error) {
	if err := ValidateKinds(kinds); err != nil {
		return false, err
	}
	target, err := Path(value)
	if err != nil {
		return false, err
	}
	for _, s := range selectors {
		if len(kinds) > 0 && !slices.Contains(kinds, s.Kind) {
			continue
		}
		root, err := Path(s.Path)
		if err != nil {
			return false, err
		}
		for _, pattern := range slices.Concat(s.Include, s.Exclude) {
			if !doublestar.ValidatePattern(pattern) {
				return false, fmt.Errorf("invalid provenance pattern %q", pattern)
			}
		}
		if under(root, target) {
			return true, nil
		}
		if !under(target, root) {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(target, root), "/")
		excluded := false
		for _, pattern := range s.Exclude {
			// Only an explicit subtree exclusion proves descendants are absent.
			base, subtree := strings.CutSuffix(pattern, "/**")
			if subtree && (relative == base || strings.HasPrefix(relative, base+"/")) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		return true, nil
	}
	return false, nil
}

// AffectedByFile is for leaf paths from a Changeset, including removed files.
func AffectedByFile(selectors []Selector, value string, kinds []string) (bool, error) {
	if err := ValidateKinds(kinds); err != nil {
		return false, err
	}
	target, err := Path(value)
	if err != nil {
		return false, err
	}
	for _, s := range selectors {
		if len(kinds) > 0 && !slices.Contains(kinds, s.Kind) {
			continue
		}
		root, err := Path(s.Path)
		if err != nil {
			return false, err
		}
		if !under(target, root) {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(target, root), "/")
		included := len(s.Include) == 0 || relative == ""
		for _, pattern := range s.Include {
			match, err := doublestar.Match(pattern, relative)
			if err != nil {
				return false, err
			}
			included = included || match
		}
		for _, pattern := range s.Exclude {
			match, err := doublestar.Match(pattern, relative)
			if err != nil {
				return false, err
			}
			if match {
				included = false
			}
		}
		if included {
			return true, nil
		}
	}
	return false, nil
}
func under(child, parent string) bool {
	return parent == "/" || child == parent || strings.HasPrefix(child, parent+"/")
}
