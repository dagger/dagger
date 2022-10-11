package go

import (
	"dagger.io/dagger"
	cdk "universe.dagger.io/alpha/aws/cdk/python"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: test: {
		_source: dagger.#Scratch & {}

		simple: {
			_image: cdk.#Image & {}

			verify: docker.#Run & {
				input: _image.output
				command: {
					name: "/bin/sh"
					args: ["-c", """
							python --version
							python --version | grep "3.8"
						"""]
				}
			}
		}

		custom: {
			_image: cdk.#Image & {
				version: "3.9"
				packages: bash: _
			}

			verify: docker.#Run & {
				input: _image.output
				command: {
					name: "/bin/bash"
					args: ["-c", """
							python --version
							python --version | grep "3.9"
						"""]
				}
			}
		}
	}
}
