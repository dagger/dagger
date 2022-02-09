package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: tests: run: {
		_build: alpine.#Build
		_image: _build.output

		// Test: run a simple shell command
		simpleShell: {
			image: alpine.#Build

			run: docker.#Run & {
				image: _image
				command: {
					name: "/bin/sh"
					args: ["-c", "echo -n hello world >> /output.txt"]
				}
			}

			verify: engine.#ReadFile & {
				input: run.output.rootfs
				path:  "/output.txt"
			}
			verify: contents: "hello world"
		}

		// Test: export a file
		exportFile: {
			image: _image
			command: {
				name: "sh"
				flags: "-c": #"""
					echo -n hello world >> /output.txt
					"""#
			}
			export: files: "/output.txt": _ & {
				// Assert content
				contents: "hello world"
			}
		}

		// Test: export a directory
		exportDirectory: {
			run: docker.#Run & {
				image: _image
				command: {
					name: "sh"
					flags: "-c": #"""
						mkdir -p /test
						echo -n hello world >> /test/output.txt
						"""#
				}
				export: directories: "/test": _
			}

			verify: engine.#ReadFile & {
				input: run.export.directories."/test".contents
				path:  "/output.txt"
			}
			verify: contents: "hello world"
		}
	}
}
