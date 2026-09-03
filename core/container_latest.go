package core

import (
	"fmt"
)

// SelectLatestContainerTag returns the greatest eligible stable release tag
// after normalizing optional v prefixes, incomplete versions, and zero-padded
// numeric components. If tags contains no eligible release, it returns
// "latest".
func SelectLatestContainerTag(tags []string) (string, error) {
	return SelectContainerTag(tags, "")
}

// SelectContainerTag returns the greatest release tag that matches
// versionQuery. An empty query selects the latest stable release and falls
// back to "latest" when no release exists. A non-empty query returns an error
// when no tag matches.
func SelectContainerTag(tags []string, versionQuery string) (string, error) {
	candidates := make([]releaseTagCandidate, 0, len(tags))
	for _, tag := range tags {
		candidates = append(candidates, releaseTagCandidate{
			Name:    tag,
			Version: tag,
		})
	}
	selected, found, err := selectReleaseTag(candidates, versionQuery)
	if err != nil {
		return "", err
	}
	if !found {
		if versionQuery != "" {
			return "", fmt.Errorf("no image tag matches version query %q", versionQuery)
		}
		return "latest", nil
	}
	return selected.Name, nil
}

// ValidateContainerLatestTag validates a tag selected by oci-latest.
func ValidateContainerLatestTag(tag string) error {
	return ValidateContainerTag(tag, "")
}

// ValidateContainerTag validates a tag selected for versionQuery.
func ValidateContainerTag(tag string, versionQuery string) error {
	if tag == "latest" {
		if versionQuery == "" {
			return nil
		}
		return fmt.Errorf("tag %q does not match version query %q", tag, versionQuery)
	}
	parsed, ok := parseReleaseTag(releaseTagCandidate{Name: tag, Version: tag})
	if !ok {
		return fmt.Errorf("tag %q is not a semantic version", tag)
	}
	query, err := parseReleaseVersionQuery(versionQuery)
	if err != nil {
		return err
	}
	if versionQuery == "" && parsed.Semver.Prerelease != "" {
		return fmt.Errorf("tag %q is not a stable semantic version", tag)
	}
	if !query.matches(parsed) {
		return fmt.Errorf("tag %q does not match version query %q", tag, versionQuery)
	}
	return nil
}
