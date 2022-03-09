package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/bash"
	"universe.dagger.io/alpine"
)


dagger.#Plan & {
	actions: {
		_image: alpine.#Build & {
			packages: {
				bash: _
			}
		}

		// Test script
		test: bash.#Run & {
			input: _image.output
			script: contents: "sleep 15 && echo test"
		}
	}
}
