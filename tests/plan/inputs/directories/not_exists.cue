package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	// should fail because path does not exist locally
	inputs: directories: test: path: "./fasdfsdfs"
	actions: verify: dagger.#ReadFile & {
		input: inputs.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local directory"
	}
}
