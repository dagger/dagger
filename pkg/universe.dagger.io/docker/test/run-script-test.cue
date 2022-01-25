package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: {
		image: alpine.#Build

		run: docker.#Run & {
			"image": image.output
			script: #"""
				echo -n $TEST_MESSAGE >> /output.txt
				"""#
			env: TEST_MESSAGE: "hello world"
		}

		verify: engine.#ReadFile & {
			input: run.output.rootfs
			path:  "/output.txt"
		} & {
			contents: "hello world"
		}
	}
}
