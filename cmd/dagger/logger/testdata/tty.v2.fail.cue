package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		_pull: docker.#Pull & {
			source: "index.docker.io/debian"
		}
		_image: _pull.output
		all: {
			hello: bash.#Run & {
				input: _image
				script: contents: """
					echo Hello enter

						sleep 0.5s

					echo Hello exit
					"""
			}
			hellobis: bash.#Run & {
				input: _image
				script: contents: """
					echo Hello bis enter
						sleep 0.2s
					echo Hello bis exit
					false
					"""
			}
		}
	}
}
