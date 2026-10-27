package engine

import (
	"fmt"
	"strconv"
	"strings"
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

// cutVersions returns the cut (PrereleaseViewsVersion), the release just
// before it, whose view every earlier v1.0.0 prerelease shares
// (basePrereleaseView), and a prerelease after it. Tests use them instead of
// literals, so moving the cut to a later beta needs no test edits.
func cutVersions(t *testing.T) (lastPreCut, cut, afterCut string) {
	t.Helper()
	return basePrereleaseView, PrereleaseViewsVersion, betaOffset(t, PrereleaseViewsVersion, 1)
}

// betaOffset returns the beta n releases after version, a vX.Y.Z-beta.N, or
// before it for a negative n.
func betaOffset(t *testing.T, version string, n int) string {
	t.Helper()
	num, ok := strings.CutPrefix(semver.Prerelease(version), "-beta.")
	require.True(t, ok, "%s is not a beta", version)
	i, err := strconv.Atoi(num)
	require.NoError(t, err, "%s is not a beta", version)
	return fmt.Sprintf("%s-beta.%d", BaseVersion(version), i+n)
}

func TestAPIViewVersion(t *testing.T) {
	lastPreCut, cut, afterCut := cutVersions(t)
	setVersion(t, cut)
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
		{version: "v1.0.0-0", view: lastPreCut},
		{version: "v1.0.0-alpha.1", view: lastPreCut},
		{version: "v1.0.0-beta.7", view: lastPreCut},
		{version: "v1.0.0-beta.19", view: lastPreCut},
		{version: "v1.0.0-beta.20", view: lastPreCut},
		{version: lastPreCut, view: lastPreCut},
		{version: lastPreCut + "+abcdef12", view: lastPreCut},

		// from PrereleaseViewsVersion on, prereleases keep their own view
		{version: cut, view: cut},
		{version: cut + "+abcdef12.dirty", view: cut},
		{version: afterCut, view: afterCut},
		{version: "v1.0.0-rc.1", view: "v1.0.0-rc.1"},
		{version: "v1.1.0-beta.1", view: "v1.1.0-beta.1"},

		// dev builds share their release's view
		{version: "v1.0.0-dev-123", view: "v1.0.0"},
		{version: "v1.0.0-dev-123+abc", view: "v1.0.0"},
		{version: "v0.0.0-010101000000-dev-deadbeefdead", view: "v0.0.0"},
		{version: "v2.0.0-dev-123", view: "v2.0.0"},

		// an empty version is the engine's own; a non-semver one is old
		{version: "", view: cut},
		{version: "foobar", view: presemverModuleVersion},
	} {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.view, APIViewVersion(tc.version))
		})
	}
}

func TestAPIVersionAtLeast(t *testing.T) {
	lastPreCut, _, _ := cutVersions(t)
	casing := IdentifierCasingVersion
	for _, tc := range []struct {
		version string
		post    bool
	}{
		{version: "v0.21.6"},
		{version: "v1.0.0-0"},
		{version: "v1.0.0-beta.19"},
		{version: "v1.0.0-beta.20"},
		{version: lastPreCut},
		{version: casing, post: true},
		{version: betaOffset(t, casing, 1), post: true},
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

// An engine refuses a module whose API view is past its own: prereleases are
// ordered from PrereleaseViewsVersion on, and dev builds share their
// release's view.
func TestMaxAPIViewCompatibility(t *testing.T) {
	lastPreCut, cut, afterCut := cutVersions(t)
	setVersion(t, cut)
	for _, tc := range []struct {
		engine     string
		version    string
		compatible bool
	}{
		{engine: cut, version: cut, compatible: true},
		{engine: cut, version: cut + "+abcdef12", compatible: true},
		{engine: cut, version: lastPreCut, compatible: true},
		{engine: cut, version: "v1.0.0-0", compatible: true},
		{engine: cut, version: "v0.21.6", compatible: true},
		{engine: cut, version: "", compatible: true},
		{engine: cut, version: afterCut},
		{engine: cut, version: "v1.0.0-rc.1"},
		{engine: cut, version: "v1.0.0"},
		{engine: cut, version: "v1.0.0-dev-123"},

		// pre-cut engines share one view, so they accept each other's
		// modules, but not the cut's
		{engine: lastPreCut, version: "v1.0.0-beta.19", compatible: true},
		{engine: "v1.0.0-beta.19", version: lastPreCut, compatible: true},
		{engine: lastPreCut, version: cut},

		// dev builds share their release's view
		{engine: "v1.0.0-dev-123", version: afterCut, compatible: true},
		{engine: "v1.0.0-dev-123", version: "v1.0.0", compatible: true},
		{engine: "v1.0.0-dev-123", version: "v1.0.0-dev-456", compatible: true},
		{engine: "v1.0.0-dev-123", version: "v1.0.1"},

		{engine: "v1.0.0", version: "v1.0.0", compatible: true},
		{engine: "v2.0.0", version: "v2.0.1"},
	} {
		t.Run(tc.engine+"/"+tc.version, func(t *testing.T) {
			require.Equal(t, tc.compatible, CheckMaxAPIViewCompatibility(tc.version, tc.engine))
		})
	}
}

// The view constants must agree: earlier v1.0.0 prereleases share one view
// below PrereleaseViewsVersion, and no gate can split them. When the cut
// slips a release, these hold only if PrereleaseViewsVersion and
// basePrereleaseView move together.
func TestPrereleaseViewConstants(t *testing.T) {
	require.True(t, semver.IsValid(PrereleaseViewsVersion))
	require.True(t, semver.IsValid(IdentifierCasingVersion))
	require.Equal(t, "v1.0.0", BaseVersion(basePrereleaseView))
	require.Negative(t, semver.Compare(basePrereleaseView, PrereleaseViewsVersion))
	require.Equal(t, betaOffset(t, PrereleaseViewsVersion, -1), basePrereleaseView,
		"basePrereleaseView must be the beta just before PrereleaseViewsVersion")
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
