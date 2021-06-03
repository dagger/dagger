package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
)

key:        dagger.#Secret @dagger(input)
passphrase: dagger.#Secret @dagger(input)
user:       string         @dagger(input)

TestRun: run: docker.#Run & {
	host:         "143.198.64.230"
	ref:          "nginx:alpine"
	"user":       user
	"passphrase": passphrase
	name:         "daggerci-test-simple-\(random)"
	"key":        key
}
