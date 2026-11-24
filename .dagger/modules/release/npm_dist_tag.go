package main

import (
	"golang.org/x/mod/semver"
)

// shouldMoveNpmLatest reports whether a freshly published version should become
// the registry's `latest`.
//
// npm publish moves `latest` only for a version with no prerelease, which is
// right for a package that has stable releases behind it. @dagger.io/core has
// only ever been a prerelease, so `latest` stayed on an old beta whose bindings
// target a different API view than the session its own `latest` resolves to —
// the mismatch the package exists to avoid, on the default install path.
//
// Two rules, and the second is not implied by the first: a prerelease never
// displaces a stable `latest`, because semver puts 1.0.1-beta.1 and 2.0.0-beta.1
// *above* 1.0.0 — only a prerelease of the same version is below it. Comparing
// alone would let the next patch line's first beta take `latest` from a release.
func shouldMoveNpmLatest(published, currentLatest string) bool {
	// Nothing to compare against: the publish set `latest` itself.
	if currentLatest == "" {
		return true
	}
	pv, cv := "v"+published, "v"+currentLatest
	if !semver.IsValid(pv) || !semver.IsValid(cv) {
		return false
	}
	if semver.Prerelease(pv) != "" && semver.Prerelease(cv) == "" {
		return false
	}
	return semver.Compare(pv, cv) > 0
}
