package util

import (
	"strings"

	"golang.org/x/mod/semver"
)

// PrereleaseVariants takes a version with a prerelease, and returns variants
// of it that should be aliased to the original one.
// Example: v0.17.0-foo.1.2.3 -> [v0.17.0-foo.1.2.3, v0.17.0-foo.1.2, v0.17.0-foo.1, v0.17.0-foo]
func PrereleaseVariants(version string) (results []string) {
	parts := strings.Split(semver.Prerelease(version), ".")
	name, parts := parts[0], parts[1:]
	for len(parts) > 0 {
		newVersion := baseVersion(version) + name
		if build := semver.Build(version); build != "" {
			newVersion += build
		}
		results = append(results, newVersion)

		name += "." + parts[0]
		parts = parts[1:]
	}
	return results
}

func baseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}
