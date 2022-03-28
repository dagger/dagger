package test

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: filesystem: "/var/run/docker.sock": read: contents: dagger.#Service

	actions: test: {
		run: cli.#Run & {
			host: client.filesystem."/var/run/docker.sock".read.contents
			command: name: "info"
		}

		differentImage: {
			_cli: docker.#Build & {
				steps: [
					alpine.#Build & {
						packages: "docker-cli": {}
					},
					docker.#Run & {
						command: {
							name: "sh"
							flags: "-c": "echo -n foobar > /test.txt"
						}
					},
				]
			}
			run: cli.#Run & {
				input: _cli.output
				host:  client.filesystem."/var/run/docker.sock".read.contents
				command: {
					name: "docker"
					args: ["info"]
				}
				export: files: "/test.txt": "foobar"
			}
		}

		// FIXME: test remote connections with `docker:dind` image
		// when we have long running tasks
	}
}
