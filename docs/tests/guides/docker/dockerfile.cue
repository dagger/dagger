package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: ".": read: contents: dagger.#FS

	actions: build: docker.#Dockerfile & {
		// This is the Dockerfile context
		source: client.filesystem.".".read.contents
	}
}
