package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	inputs: directories: test: path: "."
	actions: verify: engine.#ReadFile & {
		input: inputs.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local directory"
	}
}
