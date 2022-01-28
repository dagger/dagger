package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

// This test verify that we can correctly build a simplistic image
// using  docker.#Build
dagger.#Plan & {
	#alpineImage: "index.docker.io/alpine:3.15.0@sha256:21a3deaa0d32a8057914f36584b5288d2e5ecc984380bc0118285c70fa8c9300"

	#testValue: "hello world"

	actions: {
		image: docker.#Build & {
			steps: [
				docker.#Pull & {
					source: #alpineImage
				},
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
