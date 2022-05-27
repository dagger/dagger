package version

import (
	"fmt"
	"runtime"
)

const (
	DevelopmentVersion = "devel"
)

var (
	// Version holds the complete version number. Filled in at linking time.
	Version = DevelopmentVersion

	// Revision is filled with the VCS (e.g. git) revision being used to build
	// the program at linking time.
	Revision = ""
)

func Short() string {
	return fmt.Sprintf("dagger %s (%s)", Version, Revision)
}

func Long() string {
	return Short() + fmt.Sprintf(" %s/%s", runtime.GOOS, runtime.GOARCH)
}
