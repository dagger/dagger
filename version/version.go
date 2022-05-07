package version

import (
	"runtime/debug"
)

const (
	DevelopmentVersion = "devel"
)

var (
	// Version holds the complete version number. Filled in at linking time.
	Version = DevelopmentVersion

	// Revision is filled with the VCS (e.g. git) revision being used to build
	// the program at linking time.
	Revision string
)

func init() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		panic("no build info")
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			Revision = s.Value
			break
		}
	}
	if Revision == "" {
		panic("unable to retrieve vcs revision")
	}
}
