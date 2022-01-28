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
			command: {
				name: "/bin/sh"
				args: ["-c", "echo -n hello world >> /output.txt"]
			}
		}

		verify: engine.#ReadFile & {
			input: run.output.rootfs
			path:  "/output.txt"
		} & {
			contents: "hello world"
		}
	}
}
