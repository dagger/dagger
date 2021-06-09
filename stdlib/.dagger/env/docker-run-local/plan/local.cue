package main

import (
	"dagger.io/docker"
	"dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

run: docker.#Run & {
	name: "daggerci-test-local-\(suffix.out)"
	ref:  "hello-world"
}
