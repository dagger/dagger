package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
)

// Run with --input-file key=$HOME/.ssh/<your private server key>
key: dagger.#Artifact

TestRun: run: docker.#Run & {
	host:  "143.198.64.230"
	ref:   "nginx:alpine"
	user:  "root"
	name:  "daggerci-test-simple-\(random)"
	"key": key
}
