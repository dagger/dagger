package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		image: docker.#Build & {
			steps: [
				docker.#Dockerfile & {
					input: rootfs: inputs.directories.testdata.contents
				},
				docker.#Run & {
					always: true
					script: """
						hello >> /test.txt
						"""
				},
			]
		}

		verify: engine.#ReadFile & {
			input: image.output.rootfs
			path:  "/test.txt"
		} & {
			contents: "hello world"
		}
	}
}
