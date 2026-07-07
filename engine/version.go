package engine

import (
	"os"
	"strings"

	"golang.org/x/mod/semver"

	iversion "github.com/dagger/dagger/internal/version"
)

var (
	// Version is the engine/CLI semver, derived from internal/version.Version
	// (which embeds VERSION at build time) with a "v" prefix.
	//
	// DAGGER_VERSION overrides at init for tests.
	Version string

	// Tag is the default engine image tag. It defaults to Version for release
	// builds and Commit for dev builds.
	//
	// DAGGER_TAG overrides at init for tests.
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

	// MinimumDangV2ModuleVersion is the minimum module engine version that gets
	// Dang v2 semantics (`.{ }` is dot-block application, `.{{ }}` is
	// selection); older modules keep Dang v1 semantics (`.{ }` is selection).
	MinimumDangV2ModuleVersion = "v0.21.5"
)

var (
	presemverModuleVersion = "v0.11.9"
)

func init() {
	if Version == "" {
		Version = iversion.Version
	}

	// hack: dynamically populate version env vars
	// we use these during tests, but not really for anything else - this is
	// why it's okay to skip the previous validation
	if v, ok := os.LookupEnv(DaggerVersionEnv); ok {
		Version = cleanVersion(v)
	}
	if Tag == "" {
		Tag = Version
	}
	if v, ok := os.LookupEnv(DaggerTagEnv); ok {
		Tag = v
	}

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
	return semver.Compare(version, minVersion) >= 0
}

func CheckMaxVersionCompatibility(version string, maxVersion string) bool {
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
	prerelease := semver.Prerelease(version)
	if prerelease == "-dev" || strings.Contains(prerelease, "-dev.") || strings.Contains(prerelease, "-dev-") {
		return true
	}
	return false
}
