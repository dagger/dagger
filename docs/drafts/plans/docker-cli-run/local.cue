package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: filesystem: "/var/run/docker.sock": read: contents: dagger.#Socket

	actions: run: cli.#Run & {
		host: client.filesystem."/var/run/docker.sock".read.contents
		command: name: "info"
	}
}
