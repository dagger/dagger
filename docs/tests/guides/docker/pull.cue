package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: env: {
		REGISTRY_USER: string | "_token_"
		REGISTRY_PASS: dagger.#Secret
	}

	actions: pull: docker.#Pull & {
		source: "registry.gitlab.com/example/python:3.9"
		auth: {
			username: client.env.REGISTRY_USER
			secret:   client.env.REGISTRY_PASS
		}
	}
}
