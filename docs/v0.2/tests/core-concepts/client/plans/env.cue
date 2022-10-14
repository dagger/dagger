package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: env: {
		// load as a string
		REGISTRY_USER: string
		// load as a secret
		REGISTRY_TOKEN: dagger.#Secret
	}

	actions: pull: docker.#Pull & {
		source: "registry.example.com/image"
		auth: {
			username: client.env.REGISTRY_USER
			secret:   client.env.REGISTRY_TOKEN
		}
	}
}
