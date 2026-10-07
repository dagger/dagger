package naming

import (
	"strings"

	"golang.org/x/mod/semver"
)

// FirstVersion is the engine version that introduced identifier parsing,
// with the Initial dictionary. The engine names modules that declare an older
// engineVersion with its previous (strcase) rules instead; see
// hack/designs/identifier-casing.md.
const FirstVersion = "v1.0.0"

// releases lists every released dictionary with the engine version that
// introduced it, oldest first. A dictionary addition is a new entry here,
// never an edit to an existing dictionary: modules and clients keep the
// dictionary of the engine version they declare.
var releases = []struct {
	version string
	dict    *Dictionary
}{
	{FirstVersion, Initial},
}

// DictionaryFor returns the dictionary for a module or client that declares
// engineVersion: the newest dictionary introduced at or before it.
//
// Versions compare by their base version, like engine API views: every
// prerelease of vX.Y.Z (v1.0.0-beta.15, v1.0.0-dev-123) selects vX.Y.Z's
// dictionary. An empty version means the latest, as an empty view does.
// Versions older than FirstVersion, and invalid versions, get Initial; callers
// that need the pre-naming behavior for those check FirstVersion themselves.
func DictionaryFor(engineVersion string) *Dictionary {
	if engineVersion == "" {
		return Latest
	}
	if !strings.HasPrefix(engineVersion, "v") {
		engineVersion = "v" + engineVersion
	}
	if !semver.IsValid(engineVersion) {
		return Initial
	}
	base := baseVersion(engineVersion)
	dict := Initial
	for _, r := range releases {
		if semver.Compare(base, r.version) >= 0 {
			dict = r.dict
		}
	}
	return dict
}

// baseVersion strips a version's prerelease and build metadata.
func baseVersion(v string) string {
	v = strings.TrimSuffix(v, semver.Build(v))
	return strings.TrimSuffix(v, semver.Prerelease(v))
}
