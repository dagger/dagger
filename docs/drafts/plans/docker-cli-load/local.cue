package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: filesystem: "/var/run/docker.sock": read: contents: dagger.#Service

	actions: {
		build: docker.#Build & {
			...
		}

		load: cli.#Load & {
			image: build.output
			host:  client.filesystem."/var/run/docker.sock".read.contents
			tag:   "myimage"
		}
	}
}
