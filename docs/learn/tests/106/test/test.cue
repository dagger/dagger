package gcpcloudrun

import (
	// "alpha.dagger.io/dagger/op"
	// "alpha.dagger.io/alpine"
	"alpha.dagger.io/git"
)

// For cloudrun, only the deployment process is being checked

// Collect website from git repo
// Override source.cue Input
src: git.#Repository & {
	remote:     "https://github.com/dagger/examples"
	ref:        "main"
	subdir:     "todoapp"
}
