package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: {
		image: alpine.#Build

		run: docker.#Run & {
			"image": image.output
			command: {
				name: "sh"
				flags: "-c": #"""
					echo -n hello world >> /output.txt
					"""#
			}
			export: files: "/output.txt": _ & {
				contents: "hello world"
			}
		}
	}
}
