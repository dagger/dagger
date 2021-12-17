package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	inputs: directories: test: path: "./plan/inputs/directories"
	actions: verify: engine.#ReadFile & {
		input: inputs.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local dfsadf" // should fail with conflicting values
	}
}
