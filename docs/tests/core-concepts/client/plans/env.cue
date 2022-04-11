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
		// load as a string, using a default if not defined
		BASE_IMAGE: string | *"registry.example.com/image"
	}

	actions: pull: docker.#Pull & {
		source: client.env.BASE_IMAGE
		auth: {
			username: client.env.REGISTRY_USER
			secret:   client.env.REGISTRY_TOKEN
		}
	}
}
