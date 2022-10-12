package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: run: cli.#Run & {
		host: client.network."unix:///var/run/docker.sock".connect
		command: name: "info"
	}
}
