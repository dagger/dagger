package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

// This test verify that we can correctly build a simplistic image
// using  docker.#Build
dagger.#Plan & {
	#testValue: "hello world"

	actions: {
		image: docker.#Build & {
			steps: [
				alpine.#Build,
				docker.#Run & {
					script: """
							echo -n $TEST >> /test.txt
						"""
					env: TEST: #testValue
				},
			]
		}

		verify: engine.#ReadFile & {
			input: image.output.rootfs
			path:  "/test.txt"
		} & {
			contents: #testValue
		}
	}
}
