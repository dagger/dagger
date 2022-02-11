package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/go"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: tests: image: {
		_source: engine.#Scratch & {}

		simple: {
			_image: go.#Image & {}

			verify: docker.#Run & {
				input: _image.output
				command: {
					name: "/bin/sh"
					args: ["-c", """
							go version | grep "1.16"
						"""]
				}
			}
		}

		custom: {
			_image: go.#Image & {
				version: "1.17"
				packages: bash: _
			}

			verify: docker.#Run & {
				input: _image.output
				command: {
					name: "/bin/bash"
					args: ["-c", """
							go version | grep "1.17"
						"""]
				}
			}
		}
	}
}
