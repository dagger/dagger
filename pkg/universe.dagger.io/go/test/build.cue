package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
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
				output: "/bin/hello"
			}

			exec: docker.#Run & {
				input: _baseImage.output
				command: {
					name: "/bin/sh"
					args: ["-c", "hello >> /output.txt"]
				}
				env: NAME: "dagger"
				mounts: binary: {
					dest:     build.output
					contents: build.binary
					source:   "/bin/hello"
				}
			}

			verify: engine.#ReadFile & {
				input: exec.output.rootfs
				path:  "/output.txt"
			} & {
				contents: "Hi dagger!"
			}
		}
	}
}
