package engine

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

func TestVersionCompatibility(t *testing.T) {
	tc := []struct {
		targetVersion  string
		minVersion     string
		currentVersion string
		compatible     bool
	}{
		// fairly normal release versions
		{
			// v0.2.0 > v0.1.0
			targetVersion: "v0.2.0",
			minVersion:    "v0.1.0",
			compatible:    true,
		},
		{
			// v0.2.0 == v0.2.0
			targetVersion: "v0.2.0",
			minVersion:    "v0.2.0",
			compatible:    true,
		},
		{
			// v0.2.0 < v0.3.0
			targetVersion: "v0.2.0",
			minVersion:    "v0.3.0",
			compatible:    false,
		},

		// more complicated pre-releases
		{
			// v0.2.0-123 < v0.2.0
			targetVersion: "v0.2.0-123",
			minVersion:    "v0.2.0",
			compatible:    false,
		},
		{
			// v0.2.0-123 ~= v0.2.0
			targetVersion:  "v0.2.0-123",
			minVersion:     "v0.2.0",
			currentVersion: "v0.2.0-123",
			compatible:     true,
		},
		{
			// v0.2.0-123 !~= v0.2.0
			targetVersion:  "v0.2.0-123",
			minVersion:     "v0.2.0",
			currentVersion: "v0.2.0-456",
			compatible:     false,
		},

		// even more complicated dev versions
		{
			// v0.2.0-dev-123 ~= v0.2.0
			targetVersion:  "v0.2.0-dev-123",
			minVersion:     "v0.2.0",
			currentVersion: "v0.2.0-dev-123",
			compatible:     true,
		},
		{
			// v0.2.0-dev-123 ~= v0.2.0
			targetVersion:  "v0.2.0-dev-123",
			minVersion:     "v0.2.0",
			currentVersion: "v0.2.0-dev-456",
			compatible:     true,
		},
	}

	for _, tc := range tc {
		var name string
		if tc.compatible {
			name = fmt.Sprintf("%s is compatible with %s", tc.targetVersion, tc.minVersion)
		} else {
			name = fmt.Sprintf("%s is not compatible with %s", tc.targetVersion, tc.minVersion)
		}
		t.Run(name, func(t *testing.T) {
			// if no version is explicitly asked for, just assume it's the same
			// as the minVersion, just keeps our test cases smaller
			currentVersion := tc.currentVersion
			if currentVersion == "" {
				currentVersion = tc.minVersion
			}
			setVersion(t, currentVersion)

			// this is to replicate the logic in capping the minimum version logic
			minVersion := tc.minVersion
			if semver.Compare(currentVersion, minVersion) < 0 {
				minVersion = currentVersion
			}

			require.Equal(t, tc.compatible, CheckVersionCompatibility(tc.targetVersion, minVersion))
		})
	}
}

func TestNormalizeVersion(t *testing.T) {
	setVersion(t, "v0.3.0")
	tc := []struct {
		version string
		result  string
	}{
		{version: "v0.2.0", result: "v0.2.0"},
		{version: "0.2.0", result: "v0.2.0"},
		{version: "v0.2.0-123", result: "v0.2.0-123"},
		{version: "", result: "v0.3.0"},
		{version: "foobar", result: presemverModuleVersion},
	}
	for _, tc := range tc {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.result, NormalizeVersion(tc.version))
		})
	}
}

func TestBaseVersion(t *testing.T) {
	tc := []struct {
		version string
		result  string
	}{
		{version: "v0.2.0", result: "v0.2.0"},
		{version: "v0.2.0-123", result: "v0.2.0"},
		{version: "v0.2.0-123+456", result: "v0.2.0"},
		{version: "v0.2.0+456", result: "v0.2.0"},
		{version: "", result: ""},
		{version: "foobar", result: "foobar"},
	}
	for _, tc := range tc {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.result, BaseVersion(tc.version))
		})
	}
}

func setVersion(t *testing.T, version string) {
	t.Helper()
	oldVersion := Version
	Version = version
	t.Cleanup(func() {
		Version = oldVersion
	})
}
