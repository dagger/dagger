package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		_pull: docker.#Pull & {
			source:      "index.docker.io/debian"
			resolveMode: "preferLocal"
		}
		_image: _pull.output
		all: {
			hello: bash.#Run & {
				input: _image
				script: contents: """
					echo Hello enter
					for i in {1..10}
					do
						echo $i
						sleep 0.5s
					done

					echo Hello exit
					"""
			}
			hellobis: bash.#Run & {
				input: _image
				script: contents: """
					echo Hello bis enter
					for i in {1..10}
					do
						echo $i
						sleep 0.2s
					done
					echo Hello bis exit
					"""
			}
		}
	}
}
