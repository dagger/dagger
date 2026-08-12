package core

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"golang.org/x/mod/semver"
)

// SelectLatestContainerTag returns the greatest eligible semantic-version tag.
// If tags contains no eligible release, it returns the literal latest tag.
func SelectLatestContainerTag(tags []string, includeSubreleases bool) string {
	var bestTag, bestVersion string
	for _, tag := range tags {
		version := tag
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !semver.IsValid(version) || (!includeSubreleases && semver.Prerelease(version) != "") {
			continue
		}

		comparison := semver.Compare(version, bestVersion)
		if bestTag == "" || comparison > 0 || (comparison == 0 && tag > bestTag) {
			bestTag = tag
			bestVersion = version
		}
	}
	if bestTag == "" {
		return "latest"
	}
	return bestTag
}

// ParseContainerLatestPin validates a full tag-and-digest pin for repository.
func ParseContainerLatestPin(pin, repository string, includeSubreleases bool) (reference.Named, error) {
	ref, err := reference.ParseNormalizedNamed(pin)
	if err != nil {
		return nil, fmt.Errorf("parse container.from.latest pin %q: %w", pin, err)
	}
	if _, ok := ref.(reference.Canonical); !ok {
		return nil, fmt.Errorf("container.from.latest pin %q has no digest", pin)
	}
	tagged, ok := ref.(reference.NamedTagged)
	if !ok {
		return nil, fmt.Errorf("container.from.latest pin %q has no tag", pin)
	}

	expected, err := reference.ParseNormalizedNamed(repository)
	if err != nil {
		return nil, fmt.Errorf("parse container.from.latest repository %q: %w", repository, err)
	}
	if reference.TrimNamed(ref).Name() != reference.TrimNamed(expected).Name() {
		return nil, fmt.Errorf(
			"container.from.latest pin repository %q does not match %q",
			reference.TrimNamed(ref).Name(),
			reference.TrimNamed(expected).Name(),
		)
	}

	tag := tagged.Tag()
	if tag == "latest" {
		return ref, nil
	}
	version := tag
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return nil, fmt.Errorf(
			"container.from.latest pin tag %q is not a semantic version",
			tag,
		)
	}
	if !includeSubreleases && semver.Prerelease(version) != "" {
		return nil, fmt.Errorf(
			"container.from.latest pin tag %q is not a stable semantic version",
			tag,
		)
	}
	return ref, nil
}
