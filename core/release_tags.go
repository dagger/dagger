package core

import (
	"cmp"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// ContainerLatestReleaseLockInput marks a container.from lock entry as a
// latest-release lookup instead of an exact tag lookup.
const ContainerLatestReleaseLockInput = "latest-release"

type releaseTag struct {
	original string
	version  string
}

// SelectLatestReleaseTag returns the greatest semver tag, accepting both
// "1.2.3" and "v1.2.3".
func SelectLatestReleaseTag(tags []string, includeSubreleases bool) (string, bool) {
	candidates := make([]releaseTag, 0, len(tags))
	for _, tag := range tags {
		version := tag
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !semver.IsValid(version) {
			continue
		}
		if !isFullReleaseVersion(version) {
			continue
		}
		if !includeSubreleases && semver.Prerelease(version) != "" {
			continue
		}
		candidates = append(candidates, releaseTag{
			original: tag,
			version:  version,
		})
	}
	if len(candidates) == 0 {
		return "", false
	}

	slices.SortFunc(candidates, func(a, b releaseTag) int {
		if c := semver.Compare(a.version, b.version); c != 0 {
			return c
		}
		return cmp.Compare(a.original, b.original)
	})
	return candidates[len(candidates)-1].original, true
}

func isFullReleaseVersion(version string) bool {
	core := strings.TrimPrefix(version, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}
