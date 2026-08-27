package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/internal/semvery"
)

// releaseTagCandidate describes one tag considered for latest-release
// selection. Name and Version differ when a Git tag has a module prefix: for
// example, Name may be "sdk/go/v1.2.3" while Version is "v1.2.3".
type releaseTagCandidate struct {
	// Name is the original tag name to return when this candidate is selected.
	// It preserves prefixes and the exact spelling advertised by the source.
	Name string

	// Version is the portion of Name parsed and compared as a release version.
	// It equals Name for OCI tags and unprefixed Git tags.
	Version string

	// Target identifies the immutable object to which the tag resolves. Git
	// candidates use the advertised SHA. OCI candidates leave it empty because
	// tag listing does not resolve each tag to a digest.
	//
	// Equivalent spellings may be treated as aliases only when every candidate
	// has the same non-empty Target. Empty or differing targets remain ambiguous.
	Target string
}

// parsedReleaseTag keeps the source candidate alongside the normalized
// version and spelling details used during selection.
type parsedReleaseTag struct {
	releaseTagCandidate

	// Semver is Version parsed and normalized by the semvery package.
	Semver semvery.Version
}

func parseReleaseTag(candidate releaseTagCandidate) (parsedReleaseTag, bool) {
	version, ok := semvery.Parse(candidate.Version)
	if !ok {
		return parsedReleaseTag{}, false
	}
	return parsedReleaseTag{
		releaseTagCandidate: candidate,
		Semver:              version,
	}, true
}

func selectLatestReleaseTag(
	candidates []releaseTagCandidate,
) (releaseTagCandidate, bool, error) {
	winners := highestStableReleaseTags(candidates)
	winners = preferCompleteReleaseAliases(winners)

	switch len(winners) {
	case 0:
		return releaseTagCandidate{}, false, nil
	case 1:
		return winners[0].releaseTagCandidate, true, nil
	}

	if !haveSameNonEmptyTarget(winners) {
		return releaseTagCandidate{}, false, ambiguousReleaseTags(winners)
	}
	return preferCanonicalGitReleaseTag(winners).releaseTagCandidate, true, nil
}

func highestStableReleaseTags(
	candidates []releaseTagCandidate,
) []parsedReleaseTag {
	var winners []parsedReleaseTag
	var best semvery.Version
	found := false
	for _, candidate := range candidates {
		parsed, ok := parseReleaseTag(candidate)
		if !ok || parsed.Semver.Prerelease != "" {
			continue
		}

		if !found {
			best = parsed.Semver
			found = true
			winners = []parsedReleaseTag{parsed}
			continue
		}
		switch semvery.Compare(parsed.Semver, best) {
		case 1:
			best = parsed.Semver
			winners = []parsedReleaseTag{parsed}
		case 0:
			if !slices.ContainsFunc(winners, func(winner parsedReleaseTag) bool {
				return winner.Name == parsed.Name
			}) {
				winners = append(winners, parsed)
			}
		}
	}
	return winners
}

func haveSameNonEmptyTarget(candidates []parsedReleaseTag) bool {
	target := candidates[0].Target
	return target != "" && !slices.ContainsFunc(
		candidates[1:],
		func(candidate parsedReleaseTag) bool {
			return candidate.Target != target
		},
	)
}

func ambiguousReleaseTags(candidates []parsedReleaseTag) error {
	names := make([]string, len(candidates))
	for i, candidate := range candidates {
		names[i] = candidate.Name
	}
	slices.Sort(names)
	return fmt.Errorf(
		"ambiguous latest release %s: equivalent tags %q",
		candidates[0].Semver.Canonical,
		names,
	)
}

func preferCanonicalGitReleaseTag(
	candidates []parsedReleaseTag,
) parsedReleaseTag {
	preferred := candidates[0]
	for _, candidate := range candidates[1:] {
		if isPreferredGitReleaseTag(candidate, preferred) {
			preferred = candidate
		}
	}
	return preferred
}

// isPreferredGitReleaseTag orders equivalent tags by standard version
// structure before applying Git's conventional v prefix.
func isPreferredGitReleaseTag(a, b parsedReleaseTag) bool {
	aPadding := releaseTagPadding(a)
	bPadding := releaseTagPadding(b)
	if aPadding != bPadding {
		return aPadding < bPadding
	}
	if len(a.Semver.RawParts) != len(b.Semver.RawParts) {
		return len(a.Semver.RawParts) > len(b.Semver.RawParts)
	}
	if (a.Semver.Build == "") != (b.Semver.Build == "") {
		return a.Semver.Build == ""
	}
	if a.Semver.HasVPrefix != b.Semver.HasVPrefix {
		return a.Semver.HasVPrefix
	}
	return a.Name < b.Name
}

func releaseTagPadding(candidate parsedReleaseTag) int {
	padding := 0
	for _, part := range candidate.Semver.RawParts {
		trimmed := strings.TrimLeft(part, "0")
		padding += len(part) - len(trimmed)
		if trimmed == "" {
			padding--
		}
	}
	return padding
}

func preferCompleteReleaseAliases(
	candidates []parsedReleaseTag,
) []parsedReleaseTag {
	return slices.DeleteFunc(slices.Clone(candidates), func(candidate parsedReleaseTag) bool {
		return slices.ContainsFunc(candidates, func(other parsedReleaseTag) bool {
			return isIncompleteReleaseAlias(candidate, other)
		})
	})
}

func isIncompleteReleaseAlias(
	candidate parsedReleaseTag,
	other parsedReleaseTag,
) bool {
	if candidate.Semver.HasVPrefix != other.Semver.HasVPrefix ||
		candidate.Semver.Prerelease != other.Semver.Prerelease ||
		candidate.Semver.Build != other.Semver.Build ||
		len(candidate.Semver.RawParts) >= len(other.Semver.RawParts) {
		return false
	}
	for i, part := range candidate.Semver.RawParts {
		if part != other.Semver.RawParts[i] {
			return false
		}
	}
	for _, part := range other.Semver.RawParts[len(candidate.Semver.RawParts):] {
		if part != "0" {
			return false
		}
	}
	return true
}
