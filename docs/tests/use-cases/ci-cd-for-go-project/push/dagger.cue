package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	client: {
		// Retrieve go source code
		filesystem: ".": read: {
			contents: dagger.#FS
			include: ["go.mod", "go.sum", "**/*.go"]
		}

		// Retrieve docker password from environment
		env: DOCKER_PASSWORD: dagger.#Secret
	}

	actions: {
		// Alias to code
		_code: client.filesystem.".".read.contents

		// Improved go base image with useful tool
		_base: go.#Image & {
			packages: "build-base": version: _
		}

		// Build go project
		build: go.#Build & {
			source: _code
		}

		// Build docker image (depends on build)
		image: {
			_base: alpine.#Build & {}

			docker.#Build & {
				steps: [
					docker.#Copy & {
						input:    _base.output
						contents: build.output
						dest:     "/usr/bin"
					},
					docker.#Set & {
						config: cmd: ["/<path>/<to>/<your>/<binary>"]
					},
				]
			}
		}

		// Push image to remote registry (depends on image)
		push: {
			// Docker username
			_dockerUsername: "<docker username>"

			docker.#Push & {
				"image": image.output
				dest:    "\(_dockerUsername)/<repository>:<tag>"
				auth: {
					username: "\(_dockerUsername)"
					secret:   client.env.DOCKER_PASSWORD
				}
			}
		}
	}
}
