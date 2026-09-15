package buildinfo_test

import (
	"fmt"

	"github.com/dagger/dagger/internal/version/buildinfo"
)

// ExampleReadBuildInfo shows the typical pattern for reading VCS info from
// the running binary. Outside any special build environment this returns the
// same data as runtime/debug.ReadBuildInfo. Under build wrappers that injected
// values via -ldflags (e.g. -X github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSRevision=…),
// the injected values appear here too.
func ExampleReadBuildInfo() {
	info, ok := buildinfo.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			fmt.Println("commit:", s.Value)
		case "vcs.modified":
			fmt.Println("dirty: ", s.Value)
		case "vcs.time":
			fmt.Println("time:  ", s.Value)
		}
	}
}
