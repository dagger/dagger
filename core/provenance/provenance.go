// Package provenance matches workspace input regions without reading files.
package provenance

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/moby/patternmatcher"
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
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("workspace path contains a null byte")
	}
	clean := path.Clean(strings.ReplaceAll(value, `\`, "/"))
	// Relative parent traversal is valid when it stays inside the boundary.
	// Unlike an input scalar, this helper already uses root coordinates.
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("workspace path %q escapes workspace root", value)
	}
	return path.Clean("/" + clean), nil
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

// Pattern order is significant: a later ! pattern can undo an earlier match.
func normalizePatterns(patterns []string) []string {
	out := slices.Clone(patterns)
	if out == nil {
		return []string{}
	}
	return slices.Compact(out)
}

// AffectedByPath tests a subtree without consulting the current filesystem.
// Literal prefixes can prove subtrees disjoint. Wildcard cases that cannot be
// disproved stay selected, including paths that might name new directories.
func AffectedByPath(selectors []Selector, value string, kinds []string) (bool, error) {
	return affected(selectors, value, kinds, true)
}

// AffectedByFile tests one leaf, including an added or deleted file.
func AffectedByFile(selectors []Selector, value string, kinds []string) (bool, error) {
	return affected(selectors, value, kinds, false)
}

func affected(selectors []Selector, value string, kinds []string, subtree bool) (bool, error) {
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
		include, err := compilePatterns(s.Include)
		if err != nil {
			return false, err
		}
		exclude, err := compilePatterns(s.Exclude)
		if err != nil {
			return false, err
		}
		relative := "."
		if under(target, root) {
			relative = strings.TrimPrefix(strings.TrimPrefix(target, root), "/")
			if relative == "" {
				relative = "."
			}
		} else if !subtree || !under(root, target) {
			continue
		}
		var included, excluded bool
		if subtree {
			included, err = mayIncludeSubtree(include, relative)
			if err == nil {
				excluded, err = excludesSubtree(exclude, relative)
			}
		} else {
			included = include == nil
			if include != nil {
				included, err = include.MatchesOrParentMatches(relative)
			}
			if err == nil && exclude != nil {
				excluded, err = exclude.MatchesOrParentMatches(relative)
			}
		}
		if err != nil {
			return false, err
		}
		if included && !excluded {
			return true, nil
		}
	}
	return false, nil
}

func compilePatterns(patterns []string) (*patternmatcher.PatternMatcher, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	// CopyFilter uses the same matcher, including parent matches and ordered
	// negation. Do not replace it with a different glob dialect.
	return patternmatcher.New(patterns)
}

func mayIncludeSubtree(m *patternmatcher.PatternMatcher, relative string) (bool, error) {
	if m == nil {
		return true, nil
	}
	possible := false
	for _, p := range m.Patterns() {
		if p.Exclusion() {
			covers, err := patternCoversSubtree(p, relative)
			if err != nil {
				return false, err
			}
			if covers {
				possible = false
			}
		} else {
			mayMatch, err := patternMayMatchSubtree(p, relative)
			if err != nil {
				return false, err
			}
			possible = possible || mayMatch
		}
	}
	return possible, nil
}

func excludesSubtree(m *patternmatcher.PatternMatcher, relative string) (bool, error) {
	if m == nil {
		return false, nil
	}
	excluded := false
	for _, p := range m.Patterns() {
		if p.Exclusion() {
			mayMatch, err := patternMayMatchSubtree(p, relative)
			if err != nil {
				return false, err
			}
			if mayMatch {
				excluded = false
			}
		} else {
			covers, err := patternCoversSubtree(p, relative)
			if err != nil {
				return false, err
			}
			excluded = excluded || covers
		}
	}
	return excluded, nil
}

func singlePatternMatches(pattern, relative string) (bool, error) {
	m, err := patternmatcher.New([]string{pattern})
	if err != nil {
		return false, err
	}
	return m.MatchesOrParentMatches(relative)
}

func patternCoversSubtree(p *patternmatcher.Pattern, relative string) (bool, error) {
	pattern := p.String()
	match, err := singlePatternMatches(pattern, relative)
	if err != nil || match {
		return match, err
	}
	// A trailing /* or /** covers every child and its descendants. This also
	// handles a glob in the parent, such as **/generated/**.
	base, trimmed := strings.CutSuffix(pattern, "/**")
	if !trimmed {
		base, trimmed = strings.CutSuffix(pattern, "/*")
	}
	if trimmed {
		return singlePatternMatches(base, relative)
	}
	return false, nil
}

func patternMayMatchSubtree(p *patternmatcher.Pattern, relative string) (bool, error) {
	match, err := singlePatternMatches(p.String(), relative)
	if err != nil || match {
		return match, err
	}
	// Like CopyFilter's directory pruning, use the fixed path prefix to
	// disprove overlap. Leave ambiguous wildcard intersections selected.
	parts := strings.Split(p.String(), "/")
	fixed := parts[:0]
	for _, part := range parts {
		if strings.ContainsAny(part, `*[]?^\`) {
			break
		}
		fixed = append(fixed, part)
	}
	prefix := path.Clean(strings.Join(fixed, "/"))
	return relative == "." || prefix == "." || under(relative, prefix) || under(prefix, relative), nil
}

func under(child, parent string) bool {
	return parent == "/" || child == parent || strings.HasPrefix(child, parent+"/")
}
