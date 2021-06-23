package main

import (
	"alpha.dagger.io/docker"
	"alpha.dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

run: docker.#Run & {
	name: "daggerci-test-local-\(suffix.out)"
	ref:  "hello-world"
}
