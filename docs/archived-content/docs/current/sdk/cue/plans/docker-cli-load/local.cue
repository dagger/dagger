package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		build: docker.#Build & {
			...
		}

		load: cli.#Load & {
			image: build.output
			host:  client.network."unix:///var/run/docker.sock".connect
			tag:   "myimage"
		}
	}
}
