package engine

import (
	"fmt"
	"os"

	"golang.org/x/mod/semver"
)

var (
	EngineImageRepo              = "registry.dagger.io/engine"
	CustomEngineImageRepoEnvName = "_EXPERIMENTAL_DAGGER_ENGINE_IMAGE_REPO"
	Package                      = "github.com/dagger/dagger"
	GPUSupportEnvName            = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
)

// Version holds the complete version number. Filled in at linking time.
// For official tagged releases, this is simple semver like x.y.z
// For builds off our repo's main branch, this is the git commit sha
// For local dev builds, this is a content hash of dagger repo source from "git write-tree"
var Version string

func init() {
	// normalize Version to semver
	if semver.IsValid("v" + Version) {
		Version = "v" + Version
	}
}

func RunnerHost() (string, error) {
	if v, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"); ok {
		return v, nil
	}

	engineImageRepo := EngineImageRepo
	if customEngineImageRepo := os.Getenv(CustomEngineImageRepoEnvName); customEngineImageRepo != "" {
		engineImageRepo = customEngineImageRepo
	}

	gpuSupportEnabled := os.Getenv(GPUSupportEnvName) != ""

	tag := Version
	if gpuSupportEnabled {
		tag += "-gpu"
	}
	return fmt.Sprintf("docker-image://%s:%s", engineImageRepo, tag), nil
}
