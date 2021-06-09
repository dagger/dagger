package main

import (
	"dagger.io/docker"
	"dagger.io/dagger"
	"dagger.io/random"
)

TestRun: {
	suffix: random.#String & {
		seed: ""
	}

	run: docker.#Run & {
		name: "daggerci-test-local-\(suffix.out)"
		ref:  "hello-world"
	}
}
