package engine

import (
	"fmt"
	"os"
	"runtime/debug"

	"golang.org/x/mod/semver"
)

const (
	EngineImageRepo = "ghcr.io/dagger/engine"
)

var DevelopmentVersion = fmt.Sprintf("devel (%s)", vcsRevision())

// Version holds the complete version number. Filled in at linking time.
var Version = DevelopmentVersion

func ImageRef() string {
	// If "devel" is set, then this is a local build. Normally _EXPERIMENTAL_DAGGER_RUNNER_HOST
	// should be set to point to a runner built from local code, but we default to using "main"
	// in case it's not.
	if Version == DevelopmentVersion {
		return fmt.Sprintf("%s:main", EngineImageRepo)
	}

	// If Version is set to something besides a semver tag, then it's a build off our main branch.
	// For now, this also defaults to using the "main" tag, but in the future if we tag engine
	// images with git sha then we could use that instead
	if semver.IsValid(Version) {
		return fmt.Sprintf("%s:main", EngineImageRepo)
	}

	// Version is a semver tag, so use the engine image at that tag
	return fmt.Sprintf("%s:v%s", EngineImageRepo, Version)
}

func RunnerHost() string {
	var runnerHost string
	if v, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"); ok {
		runnerHost = v
	} else {
		runnerHost = "docker-image://" + ImageRef()
	}
	return runnerHost
}

// revision returns the VCS revision being used to build or empty string
// if none.
func vcsRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value[:9]
		}
	}

	return ""
}
