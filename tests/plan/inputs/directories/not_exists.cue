package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	// should fail because path does not exist locally
	inputs: directories: test: path: "./fasdfsdfs"
	actions: verify: engine.#ReadFile & {
		input: inputs.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local directory"
	}
}
