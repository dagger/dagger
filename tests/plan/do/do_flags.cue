package flags

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	client: filesystem: "./test_do": write: contents: actions.test.one.export.files["/output.txt"]

	actions: {
		foo: "bar"
		// Pull alpine image
		image: alpine.#Build & {
			packages: bash: {}
		}

		// Run test
		test: {
			// Which name?
			name: string | *"World"
			// What message?
			message?: string
			// How many?
			num?: float
			// on or off?
			doit: bool | *true
			// this is foo2
			foo2?: foo
			// do the first thing
			one: bash.#Run & {
				input: image.output
				script: contents: "echo Hello \(name)! \(doit) > /output.txt"
				export: files: "/output.txt": string
			}

			// Do the second thing
			two: bash.#Run & {
				input: image.output
				script: contents: "true"
			}

			// Do the third thing
			three: bash.#Run & {
				input: image.output
				script: contents: "cat /one/output.txt"
				mounts: output: {
					contents: one.export.rootfs
					dest:     "/one"
				}
			}
		}
		// !!! DON'T RUN ME !!!
		notMe: bash.#Run & {
			input: image.output
			script: contents: "false"
		}
	}
}
