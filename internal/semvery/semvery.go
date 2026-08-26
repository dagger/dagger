// Package semvery parses version strings that are conventionally SemVer but
// allow an omitted v prefix, omitted numeric components, or leading zeroes.
package semvery

import (
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// Version is a parsed SemVer-like version. It contains both the strict SemVer
// representation used for precedence and details about the source spelling
// used to choose between equivalent tags.
type Version struct {
	// Canonical is the strict, normalized SemVer representation used for
	// comparison, such as "v1.2.0" for the source spelling "01.2".
	Canonical string

	// HasVPrefix reports whether the source spelling started with "v".
	HasVPrefix bool

	// RawParts contains the numeric components exactly as written, before
	// removing leading zeroes or appending omitted components.
	RawParts []string

	// Prerelease includes the leading "-", or is empty when none was given.
	Prerelease string

	// Build includes the leading "+", or is empty when none was given.
	Build string
}

// Parse parses a SemVer-like version and returns its strict SemVer
// representation. It accepts one to three numeric components, leading zeroes,
// and an optional v prefix.
func Parse(version string) (Version, bool) {
	hasVPrefix := strings.HasPrefix(version, "v")
	version = strings.TrimPrefix(version, "v")
	core := version
	build := ""
	if buildIndex := strings.IndexByte(core, '+'); buildIndex >= 0 {
		build = core[buildIndex:]
		core = core[:buildIndex]
	}
	prerelease := ""
	if prereleaseIndex := strings.IndexByte(core, '-'); prereleaseIndex >= 0 {
		prerelease = core[prereleaseIndex:]
		core = core[:prereleaseIndex]
	}

	numericParts := strings.Split(core, ".")
	parts := slices.Clone(numericParts)
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, false
	}
	for i, part := range parts {
		if part == "" || strings.IndexFunc(part, func(r rune) bool {
			return r < '0' || r > '9'
		}) >= 0 {
			return Version{}, false
		}
		part = strings.TrimLeft(part, "0")
		if part == "" {
			part = "0"
		}
		parts[i] = part
	}
	for len(parts) < 3 {
		parts = append(parts, "0")
	}

	normalized := "v" + strings.Join(parts, ".") + prerelease + build
	if !semver.IsValid(normalized) {
		return Version{}, false
	}
	return Version{
		Canonical:  semver.Canonical(normalized),
		HasVPrefix: hasVPrefix,
		RawParts:   numericParts,
		Prerelease: prerelease,
		Build:      build,
	}, true
}

// Compare compares two parsed versions according to SemVer precedence.
func Compare(a, b Version) int {
	return semver.Compare(a.Canonical, b.Canonical)
}
