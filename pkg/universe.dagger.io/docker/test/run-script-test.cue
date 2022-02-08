package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		image: docker.#Pull & {source: "alpine"}

		run: docker.#Run & {
			"image": image.output
			command: {
				name: "sh"
				flags: "-c": "echo -n $TEST_MESSAGE >> /output.txt"
			}
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
