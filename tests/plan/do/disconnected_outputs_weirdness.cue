package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	// This is acting weird... 
	// I don't understand why `cue/flow` thinks that outputs.files.test has a dependency
	// on actions.fromAndrea.a.export._files."/output.txt"._read
	// It _does_ fail correctly, FWIW, when it reaches this code. 
	// TASK: outputs.files.test
	// DEPENDENCIES:
	//     actions.fromAndrea.a.export._files."/output.txt"._read
	//     actions.image._dag."1"._exec
	//     actions.image._dag."0"._op
	outputs: files: test: {
		dest:     "./test_do"
		contents: actions.test1.one.export.files["/output.txt"]
	}

	actions: {
		image: alpine.#Build & {
			packages: bash: {}
		}

		abcAction: {
			a: bash.#Run & {
				input: image.output
				script: contents: "echo -n 'from andrea with love' > /output.txt"
				export: files: "/output.txt": string
			}
			b: {
				x: bash.#Run & {
					input: image.output
					script: contents: "echo -n false > /output.txt"
					export: files: "/output.txt": string
				}
				if x.export.files["/output.txt"] == "false" {
					y: bash.#Run & {
						input: a.output
						script: contents: "echo -n hello from y"
						export: files: "/output.txt": "from andrea with love"
					}
				}
			}
		}

		notMe: bash.#Run & {
			input: image.output
			script: contents: "false"
		}
	}
}
