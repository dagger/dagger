package docker

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: build: docker.#Build & {
		steps: [
			docker.#Build & {
				steps: [
					docker.#Pull & {
						source: "alpine"
					},
					docker.#Run & {
						cmd: name: "ls"
					},
				]
			},
			docker.#Run & {
				cmd: name: "ls"
			},
		]
	}
}
