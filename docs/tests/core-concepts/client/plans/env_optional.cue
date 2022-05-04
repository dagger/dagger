package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: env: {
		// load as a string, using a default if not defined
		REGISTRY_USER: string | *"_token_"
		// load as a secret, but don't fail if not defined
		REGISTRY_TOKEN?: dagger.#Secret
	}

	actions: pull: docker.#Pull & {
		source: "registry.example.com/image"
		if client.env.REGISTRY_TOKEN != _|_ {
			auth: {
				username: client.env.REGISTRY_USER
				secret:   client.env.REGISTRY_TOKEN
			}
		}
	}
}
