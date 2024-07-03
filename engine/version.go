package engine

import (
	"fmt"
	"os"

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
	}

	// normalize version strings
	Version = normalizeVersion(Version)
	MinimumClientVersion = normalizeVersion(MinimumClientVersion)
	MinimumEngineVersion = normalizeVersion(MinimumEngineVersion)
}

func normalizeVersion(v string) string {
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return v
}

func CheckVersionCompatibility(version string, minVersion string) error {
	if !semver.IsValid(version) {
		return nil // probably a dev version
	}
	if !semver.IsValid(minVersion) {
		return nil // probably a dev version
	}
	if semver.Compare(version, minVersion) < 0 {
		return fmt.Errorf("version %s does not meet required version %s", version, minVersion)
	}
	return nil
}
