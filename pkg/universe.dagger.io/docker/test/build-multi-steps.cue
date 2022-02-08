package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

// This test verify that we can correctly build an image
// using docker.#Build with multiple steps executed during
// the building process
dagger.#Plan & {
	actions: {
		image: docker.#Build & {
			steps: [
				alpine.#Build,
				docker.#Run & {
					command: {
						name: "sh"
						flags: "-c": """
							echo -n hello > /bar.txt
							"""
					}
				},
				docker.#Run & {
					command: {
						name: "sh"
						flags: "-c": """
							echo -n $(cat /bar.txt) world > /foo.txt
							"""
					}
				},
				docker.#Run & {
					command: {
						name: "sh"
						flags: "-c": """
							echo -n $(cat /foo.txt) >> /test.txt
							"""
					}
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
