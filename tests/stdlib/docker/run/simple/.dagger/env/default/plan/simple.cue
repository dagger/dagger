package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
)

key: dagger.#Secret @dagger(input)

TestRun: {
	random: #Random & {}

	run: docker.#Run & {
		host:  "143.198.64.230"
		ref:   "nginx:alpine"
		user:  "root"
		name:  "daggerci-test-simple-\(random.out)"
		"key": key
	}
}
