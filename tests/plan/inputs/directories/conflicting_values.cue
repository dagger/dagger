package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: directories: test: path: "."
	actions: verify: engine.#ReadFile & {
		input: inputs.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local dfsadf" // should fail with conflicting values
	}
}
