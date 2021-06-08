package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
	"dagger.io/random"
)

key:        dagger.#Secret @dagger(input)
passphrase: dagger.#Secret @dagger(input)
user:       string         @dagger(input)

TestRun: {
	suffix: random.#String & {
		seed: ""
	}

	run: docker.#Run & {
		host:         "143.198.64.230"
		ref:          "nginx:alpine"
		"user":       user
		"passphrase": passphrase
		name:         "daggerci-test-simple-\(suffix.out)"
		"key":        key
	}
}
