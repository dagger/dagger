package releaseversion

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// Parse derives a bare semantic version from a resolved Git ref name.
func Parse(ref string) (string, error) {
	name := strings.TrimPrefix(ref, "refs/tags/")
	name = strings.TrimPrefix(name, "refs/heads/")
	version := strings.TrimPrefix(name, "v")
	core := strings.SplitN(version, "-", 2)[0]
	core = strings.SplitN(core, "+", 2)[0]
	if strings.Count(core, ".") != 2 || !semver.IsValid("v"+version) {
		return "", fmt.Errorf("source ref %q is not a semantic version", ref)
	}
	return version, nil
}

// Resolve determines the destination docs version for a source ref. An
// explicit destination is exact and rolling, so it may replace an existing
// snapshot sourced from a branch or commit.
func Resolve(ref, as string, collapsePreReleases, collapsePatch bool) (string, bool, error) {
	if as != "" {
		if _, err := semanticVersion(as); err != nil {
			return "", false, err
		}
		return as, true, nil
	}

	version, err := Parse(ref)
	if err != nil {
		return "", false, err
	}
	rolling := false
	if collapsePreReleases {
		version, rolling = CollapsePrerelease(version)
	}
	if collapsePatch {
		var patchCollapsed bool
		version, patchCollapsed = CollapsePatch(version)
		rolling = rolling || patchCollapsed
	}
	return version, rolling, nil
}

// CollapsePrerelease removes a trailing numeric prerelease identifier.
func CollapsePrerelease(version string) (string, bool) {
	v := "v" + version
	if !semver.IsValid(v) {
		return version, false
	}

	prerelease := semver.Prerelease(v)
	identifiers := strings.Split(strings.TrimPrefix(prerelease, "-"), ".")
	if len(identifiers) < 2 || !isNumeric(identifiers[len(identifiers)-1]) {
		return version, false
	}

	collapsed := "-" + strings.Join(identifiers[:len(identifiers)-1], ".")
	return strings.Replace(version, prerelease, collapsed, 1), true
}

// CollapsePatch removes the patch component from a version.
func CollapsePatch(version string) (string, bool) {
	v := "v" + version
	if !semver.IsValid(v) {
		return version, false
	}

	build := semver.Build(v)
	prerelease := semver.Prerelease(v)
	core := strings.TrimSuffix(strings.TrimSuffix(version, build), prerelease)
	patch := strings.LastIndex(core, ".")
	if strings.Count(core, ".") != 2 || patch == -1 {
		return version, false
	}
	return core[:patch] + prerelease + build, true
}

// Rename replaces one version in a docs version list without reordering it.
func Rename(versions []string, from, to string) ([]string, error) {
	if _, err := semanticVersion(from); err != nil {
		return nil, err
	}
	if _, err := semanticVersion(to); err != nil {
		return nil, err
	}
	if from == to {
		return nil, fmt.Errorf("docs versions are both %q", from)
	}

	found := false
	result := append([]string(nil), versions...)
	for i, version := range result {
		if version == to {
			return nil, fmt.Errorf("docs version %q already exists", to)
		}
		if version == from {
			if found {
				return nil, fmt.Errorf("docs version %q appears more than once", from)
			}
			found = true
			result[i] = to
		}
	}
	if !found {
		return nil, fmt.Errorf("docs version %q does not exist", from)
	}
	return result, nil
}

// SortNewestFirst orders docs versions by semantic version. Docs versions may
// omit a patch component, as in 1.0 or 1.0-beta.
func SortNewestFirst(versions []string) ([]string, error) {
	type sortableVersion struct {
		name     string
		semantic string
	}

	sorted := make([]sortableVersion, len(versions))
	for i, version := range versions {
		semantic, err := semanticVersion(version)
		if err != nil {
			return nil, err
		}
		sorted[i] = sortableVersion{name: version, semantic: semantic}
	}

	sort.SliceStable(sorted, func(i, j int) bool {
		return semver.Compare(sorted[i].semantic, sorted[j].semantic) > 0
	})

	result := make([]string, len(sorted))
	for i, version := range sorted {
		result[i] = version.name
	}
	return result, nil
}

func semanticVersion(version string) (string, error) {
	v := "v" + version
	if semver.IsValid(v) {
		return v, nil
	}

	coreEnd := strings.IndexAny(version, "-+")
	if coreEnd == -1 {
		coreEnd = len(version)
	}
	if strings.Count(version[:coreEnd], ".") == 1 {
		v = "v" + version[:coreEnd] + ".0" + version[coreEnd:]
	}
	if !semver.IsValid(v) {
		return "", fmt.Errorf("docs version %q is not semantic", version)
	}
	return v, nil
}

func isNumeric(identifier string) bool {
	if identifier == "" {
		return false
	}
	for _, char := range identifier {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
