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

// OmitZeroPatch removes a zero patch component from a version.
func OmitZeroPatch(version string) string {
	v := "v" + version
	if !semver.IsValid(v) {
		return version
	}

	build := semver.Build(v)
	prerelease := semver.Prerelease(v)
	core := strings.TrimSuffix(strings.TrimSuffix(version, build), prerelease)
	if strings.Count(core, ".") != 2 || !strings.HasSuffix(core, ".0") {
		return version
	}
	return strings.TrimSuffix(core, ".0") + prerelease + build
}

// SortNewestFirst orders docs versions by semantic version. Docs versions may
// omit a zero patch component, as in 1.0 or 1.0-beta.
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
