package engine

import (
	"fmt"
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
	// - For official tagged releases, this is simple semver like x.y.z
	// - For builds off our repo's main branch, this is the git commit sha
	// - For local dev builds, this is a content hash of the source directory
	Version string

	// MinimumEngineVersion is used by the client to determine the minimum
	// allowed engine version that can be used by that client.
	MinimumEngineVersion = "v0.12.0"

	// MinimumClientVersion is used by the engine to determine the minimum
	// allowed client version that can connect to that engine.
	MinimumClientVersion = "v0.12.0"

	// MinimumModuleVersion is used by the engine to determine the minimum
	// allowed module engine version that can connect to this engine.
	//
	// Set to v0.9.9, because this was when the engineVersion field was
	// introduced - if it's present and not a dev version, it must be higher
	// than v0.9.9.
	MinimumModuleVersion = "v0.9.9"
)

func init() {
	// hack: dynamically populate version env vars
	// we use these during tests, but not really for anything else
	if v, ok := os.LookupEnv(DaggerVersionEnv); ok {
		Version = v
	}
	if v, ok := os.LookupEnv(DaggerMinimumVersionEnv); ok {
		MinimumClientVersion = v
		MinimumEngineVersion = v
		MinimumModuleVersion = v
	}

	// normalize version strings
	Version = normalizeVersion(Version)
	MinimumClientVersion = normalizeVersion(MinimumClientVersion)
	MinimumEngineVersion = normalizeVersion(MinimumEngineVersion)
	MinimumModuleVersion = normalizeVersion(MinimumModuleVersion)
}

func normalizeVersion(v string) string {
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return v
}

func CheckVersionCompatibility(version string, minVersion string) error {
	if isDevVersion(version) || isDevVersion(minVersion) {
		// assume that a dev version in either direction is *always* compatible
		return nil
	}

	if semver.Compare(version, minVersion) < 0 {
		return fmt.Errorf("version %s does not meet required version %s", version, minVersion)
	}
	return nil
}

func isDevVersion(version string) bool {
	if !semver.IsValid(version) {
		// probably an old dev version
		return true
	}

	// a more "modern" dev version
	parts := strings.Split(semver.Prerelease(version), "-")
	return slices.Contains(parts, "dev")
}
