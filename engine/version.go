package engine

import (
	"os"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

var (
	// Version holds the complete version number.
	//
	// Note: this is filled at link-time.
	//
	// - For official tagged releases, this is simple semver like vX.Y.Z
	// - For builds off our repo's main branch, this is a pre-release of the
	//   form vX.Y.Z-<timestamp>-<commit>
	// - For local dev builds with no other specified version, this is a
	//   pre-release of the form vX.Y.Z-<timestamp>-dev-<dirhash>
	Version string

	// Tag holds the tag that the respective engine version is tagged with.
	//
	// Note: this is filled at link-time.
	//
	// - For official tagged releases, this is simple semver like vX.Y.Z
	// - For untagged builds, this is a commit sha for the last known commit from main
	// - For dev builds, this is the last known commit from main (or maybe empty)
	Tag string

	// MinimumEngineVersion is used by the client to determine the minimum
	// allowed engine version that can be used by that client.
	MinimumEngineVersion = "v0.19.0"

	// MinimumClientVersion is used by the engine to determine the minimum
	// allowed client version that can connect to that engine.
	MinimumClientVersion = "v0.19.0"

	// MinimumModuleVersion is used by the engine to determine the minimum
	// allowed module engine version that can connect to this engine.
	//
	// Set to v0.9.9, because this was when the engineVersion field was
	// introduced - if it's present and not a dev version, it must be higher
	// than v0.9.9.
	MinimumModuleVersion = "v0.9.9"

	// MinimumDefaultFunctionCachingModuleVersion is the minimum module version at which
	// we will enable default function caching behavior.
	MinimumDefaultFunctionCachingModuleVersion = "v0.19.4"
)

var (
	presemverModuleVersion = "v0.11.9"
)

func init() {
	// The minimum version is greater than our current version this is weird,
	// and shouldn't generally be intentional - but can happen if we set it to
	// vX.Y.Z in anticipation of the next release being vX.Y.Z.
	//
	// To avoid this causing huge issues in dev builds no longer being able
	// to connect to each other, in this scenario, we cap the minVersion at
	// the current version.
	if semver.Compare(Version, MinimumClientVersion) < 0 {
		MinimumClientVersion = Version
	}
	if semver.Compare(Version, MinimumEngineVersion) < 0 {
		MinimumEngineVersion = Version
	}
	if semver.Compare(Version, MinimumModuleVersion) < 0 {
		MinimumModuleVersion = Version
	}

	// hack: dynamically populate version env vars
	// we use these during tests, but not really for anything else - this is
	// why it's okay to skip the previous validation
	if v, ok := os.LookupEnv(DaggerVersionEnv); ok {
		Version = cleanVersion(v)
	}
	if v, ok := os.LookupEnv(DaggerMinimumVersionEnv); ok {
		MinimumClientVersion = cleanVersion(v)
		MinimumEngineVersion = cleanVersion(v)
		MinimumModuleVersion = cleanVersion(v)
	}
}

func cleanVersion(v string) string {
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return v
}

func CheckVersionCompatibility(version string, minVersion string) bool {
	if IsDevVersion(version) && IsDevVersion(Version) {
		// Both our version and our target version are dev versions - in this
		// case, strip pre-release info from our target, we should pretend it's
		// just the real thing here.
		version = BaseVersion(version)
	}
	return semver.Compare(version, minVersion) >= 0
}

func CheckMaxVersionCompatibility(version string, maxVersion string) bool {
	if IsDevVersion(version) && IsDevVersion(Version) {
		// see CheckVersionCompatibility
		version = BaseVersion(version)
	}
	return semver.Compare(version, maxVersion) <= 0
}

func NormalizeVersion(version string) string {
	version = cleanVersion(version)
	switch {
	case version == "":
		// if the target version is empty, this is weird, but probably because
		// someone did a manual build - so just assume they know what they're
		// doing, and set it to be the latest we know about
		return Version
	case !semver.IsValid(version):
		// older versions of dagger don't all use semver, so if it's a
		// non-semver version, assume it's v0.11.9 (not perfect, but it's a
		// pretty good guess)
		return presemverModuleVersion
	default:
		return version
	}
}

func BaseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}

func IsDevVersion(version string) bool {
	if version == "" {
		return true
	}
	return slices.Contains(strings.Split(semver.Prerelease(version), "-"), "dev")
}
