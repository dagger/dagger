package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

run: #Run & {
	name:   "daggerci-test-ports-\(suffix.out)"
	ref:    "nginx"
	socket: dagger.#Stream & {unix: "/var/run/docker.sock"}
	ports: ["8080:80"]
}
