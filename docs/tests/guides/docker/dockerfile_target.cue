package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: ".": read: contents: dagger.#FS

	actions: {
		build: docker.#Dockerfile & {
			source: client.filesystem.".".read.contents
			// Use the stage `FROM base as build`
			target: "build"
		}

		run: docker.#Dockerfile & {
			source: client.filesystem.".".read.contents
			// Use the stage `FROM base as run`
			target: "run"
		}

		// push images to registry
	}
}
