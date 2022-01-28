package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: {
		base: alpine.#Build

		run: docker.#Run & {
			command: {
				name: "sh"
				flags: "-c": "echo -n hello world >> /output.txt"
			}
			image: base.output
			export: files: "/output.txt": _ & {
				contents: "hello world"
			}
		}
	}
}
