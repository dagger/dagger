package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	client: {
		filesystem: ".": read: {
			contents: dagger.#FS
			include: ["go.mod", "go.sum", "**/*.go"]
		}

		env: DOCKER_PASSWORD: dagger.#Secret
	}

	actions: {
		// Alias to code
		_code: client.filesystem.".".read.contents

		// Improve go base image with useful tool
		// Enable cgo by installing build-base
		_base: go.#Image & {
			packages: {
				"build-base": version: _
				bash: version:         _
			}
		}

		// Run go unit tests
		unitTest: go.#Test & {
			source:  _code
			package: "./..."
			input:   _base.output
		}

		// Build go project
		build: go.#Build & {
			source: _code
		}

		// Build docker image (depends on build)
		image: docker.#Build & {
			steps: [
				alpine.#Build,
				docker.#Copy & {
					contents: build.output
					dest:     "/usr/bin"
				},
				docker.#Set & {
					config: cmd: ["<path/to/binary>"]
				},
			]
		}

		// Push image to remote registry (depends on image)
		push: {
			// Docker username
			_dockerUsername: "<my_username>"

			docker.#Push & {
				"image": image.output
				dest:    "\(_dockerUsername)/<my_repository>"
				auth: {
					username: _dockerUsername
					secret:   client.env.DOCKER_PASSWORD
				}
			}
		}
	}
}
