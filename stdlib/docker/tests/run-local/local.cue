package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

dockersocket: dagger.#Stream & dagger.#Input

suffix: random.#String & {
	seed: ""
}

run: #Run & {
	name:   "daggerci-test-local-\(suffix.out)"
	ref:    "hello-world"
	socket: dockersocket
}
