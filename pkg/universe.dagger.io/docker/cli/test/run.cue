package test

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: filesystem: "/var/run/docker.sock": read: contents: dagger.#Socket

	actions: test: {
		run: cli.#Run & {
			host: client.filesystem."/var/run/docker.sock".read.contents
			command: name: "info"
		}

		differentImage: {
			_cli: alpine.#Build & {
				packages: {
					bash: {}
					"docker-cli": {}
				}
			}
			run: cli.#RunSocket & {
				input: _cli.output
				host:  client.filesystem."/var/run/docker.sock".read.contents
				command: {
					name: "docker"
					args: ["info"]
				}
			}
		}

		// FIXME: test remote connections with `docker:dind` image
		// when we have long running tasks
	}
}
