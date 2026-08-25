package core

import (
	"fmt"
)

// SelectLatestContainerTag returns the greatest eligible stable release tag
// after normalizing optional v prefixes, incomplete versions, and zero-padded
// numeric components. If tags contains no eligible release, it returns
// "latest".
func SelectLatestContainerTag(tags []string) (string, error) {
	candidates := make([]releaseTagCandidate, 0, len(tags))
	for _, tag := range tags {
		candidates = append(candidates, releaseTagCandidate{
			Name:    tag,
			Version: tag,
		})
	}
	selected, found, err := selectLatestReleaseTag(candidates)
	if err != nil {
		return "", err
	}
	if !found {
		return "latest", nil
	}
	return selected.Name, nil
}

// ValidateContainerLatestTag validates a tag selected by oci-latest.
func ValidateContainerLatestTag(tag string) error {
	if tag == "latest" {
		return nil
	}
	parsed, ok := parseReleaseTag(releaseTagCandidate{Name: tag, Version: tag})
	if !ok {
		return fmt.Errorf("tag %q is not a semantic version", tag)
	}
	if parsed.Semver.Prerelease != "" {
		return fmt.Errorf("tag %q is not a stable semantic version", tag)
	}
	return nil
}
