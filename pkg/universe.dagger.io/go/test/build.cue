package go

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	inputs: directories: testhello: path: "./data/hello"

	actions: tests: build: {
		_baseImage: alpine.#Build

		simple: {
			build: go.#Build & {
				source: inputs.directories.testhello.contents
			}

			exec: docker.#Run & {
				input: _baseImage.output
				command: {
					name: "/bin/sh"
					args: ["-c", "/bin/hello >> /output.txt"]
				}
				env: NAME: "dagger"
				mounts: binary: {
					dest:     "/bin/hello"
					contents: build.output
					source:   "/test"
				}
			}

			verify: dagger.#ReadFile & {
				input: exec.output.rootfs
				path:  "/output.txt"
			} & {
				contents: "Hi dagger!"
			}
		}
	}
}
