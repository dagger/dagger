package dagql

import (
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/dagql/call"
)

// AfterVersion is a view that checks if a target version is greater than *or*
// equal to the filtered version.
type AfterVersion string

var _ ViewFilter = AfterVersion("")

func (minVersion AfterVersion) Contains(version call.View) bool {
	if version == "" {
		return true
	}
	return semver.Compare(string(version), string(minVersion)) >= 0
}

// BeforeVersion is a view that checks if a target version is less than the
// filtered version.
type BeforeVersion string

var _ ViewFilter = BeforeVersion("")

func (maxVersion BeforeVersion) Contains(version call.View) bool {
	if version == "" {
		return false
	}
	return semver.Compare(string(version), string(maxVersion)) < 0
}
