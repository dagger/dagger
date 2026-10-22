package engine

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

// TestVersionCompatibility exercises CheckVersionCompatibility, which compares
// BASE versions (prerelease + build metadata stripped). The consequence these
// cases pin down: a prerelease is interchangeable with its release and with
// other prereleases of the same base, while different bases still differ.
//
// Harness note: currentVersion defaults to minVersion and exists only to
// replicate init()'s min-version capping (min is lowered to currentVersion when
// currentVersion is the older of the two). The compatibility check itself only
// looks at (targetVersion, effective minVersion) — it does not consult the
// running Version — so setVersion here just feeds that capping step.
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
			// same base v0.2.0: the prerelease is treated as its release
			targetVersion: "v0.2.0-123",
			minVersion:    "v0.2.0",
			compatible:    true,
		},
		{
			// same base v0.2.0
			targetVersion:  "v0.2.0-123",
			minVersion:     "v0.2.0",
			currentVersion: "v0.2.0-123",
			compatible:     true,
		},
		{
			// two prereleases sharing base v0.2.0 are compatible
			targetVersion:  "v0.2.0-123",
			minVersion:     "v0.2.0",
			currentVersion: "v0.2.0-456",
			compatible:     true,
		},

		// dev builds: differing -dev suffixes, same base
		{
			targetVersion:  "v0.0.0-dev-123",
			minVersion:     "v0.0.0",
			currentVersion: "v0.0.0-dev-123",
			compatible:     true,
		},
		{
			// same base v0.0.0 despite different -dev suffixes
			targetVersion:  "v0.0.0-dev-123",
			minVersion:     "v0.0.0",
			currentVersion: "v0.0.0-dev-456",
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
		{version: "v1.0.0-dev", result: "v1.0.0-dev"},
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
		{version: "v0.0.0-010101000000-dev-deadbeefdead", result: "v0.0.0"},
	}
	for _, tc := range tc {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.result, BaseVersion(tc.version))
		})
	}
}

func TestAPIViewVersion(t *testing.T) {
	setVersion(t, "v1.0.0-beta.21")
	for _, tc := range []struct {
		version string
		view    string
	}{
		// releases keep their view
		{version: "v0.21.6", view: "v0.21.6"},
		{version: "0.21.6", view: "v0.21.6"},
		{version: "v1.0.0", view: "v1.0.0"},
		{version: "v1.0.0+abcdef12.dirty", view: "v1.0.0"},
		{version: "v1.2.3", view: "v1.2.3"},

		// prereleases before PrereleaseViewsVersion are base-granular, except
		// v1.0.0's, which share basePrereleaseView: v1.0.0 is the release's
		{version: "v0.21.0-rc1", view: "v0.21.0"},
		{version: "v1.0.0-0", view: "v1.0.0-beta.20"},
		{version: "v1.0.0-alpha.1", view: "v1.0.0-beta.20"},
		{version: "v1.0.0-beta.7", view: "v1.0.0-beta.20"},
		{version: "v1.0.0-beta.19", view: "v1.0.0-beta.20"},
		{version: "v1.0.0-beta.20", view: "v1.0.0-beta.20"},
		{version: "v1.0.0-beta.20+abcdef12", view: "v1.0.0-beta.20"},

		// from PrereleaseViewsVersion on, prereleases keep their own view
		{version: "v1.0.0-beta.21", view: "v1.0.0-beta.21"},
		{version: "v1.0.0-beta.21+abcdef12.dirty", view: "v1.0.0-beta.21"},
		{version: "v1.0.0-beta.22", view: "v1.0.0-beta.22"},
		{version: "v1.0.0-rc.1", view: "v1.0.0-rc.1"},
		{version: "v1.1.0-beta.1", view: "v1.1.0-beta.1"},

		// dev builds share their release's view
		{version: "v1.0.0-dev-123", view: "v1.0.0"},
		{version: "v1.0.0-dev-123+abc", view: "v1.0.0"},
		{version: "v0.0.0-010101000000-dev-deadbeefdead", view: "v0.0.0"},
		{version: "v2.0.0-dev-123", view: "v2.0.0"},

		// an empty version is the engine's own; a non-semver one is old
		{version: "", view: "v1.0.0-beta.21"},
		{version: "foobar", view: presemverModuleVersion},
	} {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.view, APIViewVersion(tc.version))
		})
	}
}

func TestAPIVersionAtLeast(t *testing.T) {
	// a released engine: the v1.0.0 module clamp (ModuleAPIViewVersion) is
	// off
	setVersion(t, "v1.0.1")
	for _, tc := range []struct {
		version string
		post    bool
	}{
		{version: "v0.21.6"},
		{version: "v1.0.0-0"},
		{version: "v1.0.0-beta.19"},
		{version: "v1.0.0-beta.20"},
		{version: "v1.0.0-beta.21", post: true},
		{version: "v1.0.0-beta.22", post: true},
		{version: "v1.0.0-rc.1", post: true},
		{version: "v1.0.0-dev-123", post: true},
		{version: "v1.0.0", post: true},
		{version: "v1.0.1", post: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.post, APIVersionAtLeast(tc.version, IdentifierCasingVersion))
			// gates set while views dropped prereleases still apply to
			// every v1.0.0 prerelease
			v1 := semver.Compare(BaseVersion(tc.version), "v1.0.0") >= 0
			require.Equal(t, v1, APIVersionAtLeast(tc.version, "v1.0.0-0"))
			require.Equal(t, v1, APIVersionAtLeast(tc.version, "v1.0.0-beta.17"))
		})
	}
}

// While the engine is a v1.0.0 prerelease or dev build, a module declaring
// exactly v1.0.0 gets the view of the earlier prereleases; clients, and
// modules on a released engine, get the release's.
func TestModuleAPIViewVersion(t *testing.T) {
	for _, tc := range []struct {
		engine  string
		version string
		view    string
	}{
		{engine: "v1.0.0-beta.21", version: "v1.0.0", view: "v1.0.0-beta.20"},
		{engine: "v1.0.0-beta.21", version: "1.0.0", view: "v1.0.0-beta.20"},
		{engine: "v1.0.0-beta.21", version: "v1.0.0+abc", view: "v1.0.0-beta.20"},
		{engine: "v1.0.0-beta.22", version: "v1.0.0", view: "v1.0.0-beta.20"},
		{engine: "v1.0.0-dev-123", version: "v1.0.0", view: "v1.0.0-beta.20"},
		{engine: "v1.0.0", version: "v1.0.0", view: "v1.0.0"},
		{engine: "v1.0.1", version: "v1.0.0", view: "v1.0.0"},
		{engine: "v0.21.6", version: "v1.0.0", view: "v1.0.0"},

		// only exactly v1.0.0 is clamped
		{engine: "v1.0.0-beta.21", version: "v1.0.0-dev-123", view: "v1.0.0"},
		{engine: "v1.0.0-beta.21", version: "v1.0.0-beta.21", view: "v1.0.0-beta.21"},
		{engine: "v1.0.0-beta.21", version: "v1.0.0-beta.19", view: "v1.0.0-beta.20"},
		{engine: "v1.0.0-beta.21", version: "v1.0.1", view: "v1.0.1"},
		{engine: "v1.0.0-beta.21", version: "v0.21.6", view: "v0.21.6"},
		{engine: "v1.0.0-beta.21", version: "", view: "v1.0.0-beta.21"},
	} {
		t.Run(tc.engine+"/"+tc.version, func(t *testing.T) {
			setVersion(t, tc.engine)
			require.Equal(t, tc.view, ModuleAPIViewVersion(tc.version))
			require.Equal(t, semver.Compare(tc.view, IdentifierCasingVersion) >= 0,
				APIVersionAtLeast(tc.version, IdentifierCasingVersion))
		})
	}
	// clients are not clamped
	setVersion(t, "v1.0.0-beta.21")
	require.Equal(t, "v1.0.0", APIViewVersion("v1.0.0"))
}

// The view constants must agree: earlier v1.0.0 prereleases share one view
// below PrereleaseViewsVersion, and no gate can split them.
func TestPrereleaseViewConstants(t *testing.T) {
	require.True(t, semver.IsValid(PrereleaseViewsVersion))
	require.True(t, semver.IsValid(IdentifierCasingVersion))
	require.Equal(t, "v1.0.0", BaseVersion(basePrereleaseView))
	require.Negative(t, semver.Compare(basePrereleaseView, PrereleaseViewsVersion))
	require.GreaterOrEqual(t, semver.Compare(IdentifierCasingVersion, PrereleaseViewsVersion), 0,
		"IdentifierCasingVersion must not be older than PrereleaseViewsVersion")
}

func setVersion(t *testing.T, version string) {
	t.Helper()
	oldVersion := Version
	Version = version
	t.Cleanup(func() {
		Version = oldVersion
	})
}
