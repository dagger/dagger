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
	name:   "daggerci-test-ports-\(suffix.out)"
	ref:    "nginx"
	socket: dockersocket
	ports: ["8080:80"]
}
