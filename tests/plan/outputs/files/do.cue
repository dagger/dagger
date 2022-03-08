package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	client: filesystem: "./test_do": write: contents: actions.test.one.export.files["/output.txt"]

	actions: {
		image: alpine.#Build & {
			packages: bash: {}
		}

		test: {
			one: bash.#Run & {
				input: image.output
				script: contents: "echo Hello World! > /output.txt"
				export: files: "/output.txt": string
			}

			two: bash.#Run & {
				input: image.output
				script: contents: "true"
			}

			three: bash.#Run & {
				input: image.output
				script: contents: "cat /one/output.txt"
				mounts: output: {
					contents: one.export.rootfs
					dest:     "/one"
				}
			}
		}

		notMe: bash.#Run & {
			input: image.output
			script: contents: "false"
		}
	}
}
