package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

dockersocket: dagger.#Stream & dagger.#Input

source: dagger.#Artifact & dagger.#Input

TestLoad: {
	suffix: random.#String & {
		seed: ""
	}

	image: #Build & {
		"source": source
	}

	load: #Load & {
		tag:    "daggerci-image-load-\(suffix.out)"
		source: image
		socket: dockersocket
	}

	run: #Run & {
		name:   "daggerci-container-load-\(suffix.out)"
		ref:    load.id
		socket: dockersocket
	}
}
