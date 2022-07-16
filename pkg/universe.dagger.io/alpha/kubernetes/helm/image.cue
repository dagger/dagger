package helm

import (
	"universe.dagger.io/docker"
)

#Image: {
	version: string | *"latest"

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "index.docker.io/alpine/helm:\(version)"
			},
			docker.#Set & {
				config: workdir: "/workspace"
			},
		]
	}
}
