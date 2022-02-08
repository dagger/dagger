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
				name: "sh"
				flags: "-c": #"""
					mkdir -p test
					echo -n hello world >> /test/output.txt
					"""#
			}
			export: {
				directories: "/test":      _
				files: "/test/output.txt": _ & {
					contents: "hello world"
				}
			}
		} & {
			completed: true
			success:   true
		}

		verify: engine.#ReadFile & {
			input: run.export.directories."/test".contents
			path:  "/output.txt"
		} & {
			contents: run.export.files."/test/output.txt".contents
		}
	}
}
