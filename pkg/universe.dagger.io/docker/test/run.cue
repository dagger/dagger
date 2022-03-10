package docker

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: test: run: {
		_build: alpine.#Build
		_image: _build.output

		// Test: run a simple shell command
		simpleShell: {
			image: alpine.#Build

			run: docker.#Run & {
				input: _image
				command: {
					name: "/bin/sh"
					args: ["-c", "echo -n hello world >> /output.txt"]
				}
			}

			verify: dagger.#ReadFile & {
				input: run.output.rootfs
				path:  "/output.txt"
			}
			verify: contents: "hello world"
		}

		// Test: export a file
		exportFile: {
			run: docker.#Run & {
				input: _image
				command: {
					name: "sh"
					flags: "-c": #"""
						echo -n hello world >> /output.txt
						"""#
				}
				export: files: "/output.txt": string & "hello world"
			}
		}

		// Test: export a directory
		exportDirectory: {
			run: docker.#Run & {
				input: _image
				command: {
					name: "sh"
					flags: "-c": #"""
						mkdir -p /test
						echo -n hello world >> /test/output.txt
						"""#
				}
				export: directories: "/test": _
			}

			verify: dagger.#ReadFile & {
				input: run.export.directories."/test"
				path:  "/output.txt"
			}
			verify: contents: "hello world"
		}
	}
}
