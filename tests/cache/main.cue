package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: {
		image: alpine.#Build & {}

		// Test script
		test: docker.#Run & {
			input: image.output
			command: {
				name: "/bin/sh"
				args: ["-c", "sleep 10 && echo test"]
			}
		}
	}
}
