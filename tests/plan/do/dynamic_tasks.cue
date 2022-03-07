package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	outputs: files: andrea: {
		dest:     "./andrea_do"
		contents: actions.test.b.y.export.files["/output.txt"]
	}

	actions: {
		image: alpine.#Build & {
			packages: bash: {}
		}

		test: {
			a: bash.#Run & {
				input: image.output
				script: contents: "echo -n 'from andrea with love' > /output.txt"
				export: files: "/output.txt": string
			}
			b: {
				x: bash.#Run & {
					input: image.output
					script: contents: "echo -n testing > /output.txt"
					export: files: "/output.txt": string
				}
				// This fails. Building the Actions lookup table breaks
				if x.export.files["/output.txt"] == "testing" {
					y: bash.#Run & {
						input: a.output
						script: contents: "echo -n hello from y"
						export: files: "/output.txt": string
					}
				}
			}
		}

		// notMe: bash.#Run & {
		//  input: image.output
		//  script: contents: "false"
		// }
	}
}
