package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		pull: docker.#Pull & {
			source: "alpine"
		}
		push: docker.#Push & {
			image: pull.output
			dest:  "localhost:5042/alpine"
		}
	}
}
