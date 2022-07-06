package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

const (
	DevelopmentVersion = "devel"
)

// Version holds the complete version number. Filled in at linking time.
var Version = DevelopmentVersion

// Revision returns the VCS revision being used to build or empty string
// if none.
func Revision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}

	return ""
}

func Short() string {
	return fmt.Sprintf("dagger %s (%s)", Version, Revision())
}

func Long() string {
	return fmt.Sprintf("%s %s/%s", Short(), runtime.GOOS, runtime.GOARCH)
}
