package main

import (
	"dagger.io/docker"
	"dagger.io/dagger"
)

TestRun: {
	random: #Random & {}

	run: docker.#Run & {
		name: "daggerci-test-local-\(random.out)"
		ref:  "hello-world"
	}
}
