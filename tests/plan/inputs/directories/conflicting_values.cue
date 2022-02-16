package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: test: path: "."
	actions: verify: dagger.#ReadFile & {
		input: inputs.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local dfsadf" // should fail with conflicting values
	}
}
