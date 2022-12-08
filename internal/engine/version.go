package engine

import (
	"fmt"
	"os"
)

const (
	DevelopmentVersion = "devel"
	EngineImageRepo    = "ghcr.io/dagger/engine"
)

// Version holds the complete version number. Filled in at linking time.
var Version = DevelopmentVersion

func ImageRef() string {
	// if this is a release, use the release image
	if Version != DevelopmentVersion {
		return fmt.Sprintf("%s:v%s", EngineImageRepo, Version)
	}
	// fallback to using the latest image from main
	return fmt.Sprintf("%s:main", EngineImageRepo)
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
